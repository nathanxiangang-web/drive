package cloud

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/BurntSushi/toml"
	"github.com/SheltonZhu/115driver/pkg/driver"
	"golang.org/x/time/rate"
)

var Unavailable = errors.New("115 目录服务暂不可用")
var Missing = errors.New("文件或目录不存在")

const PageSize = 50

var cloudID = regexp.MustCompile(`^[0-9]{1,32}$`)

type Item struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Directory bool   `json:"directory"`
	Size      int64  `json:"size"`
}

type Page struct {
	Items  []Item `json:"items"`
	Offset int    `json:"offset"`
	Next   int    `json:"next"`
	CID    string `json:"-"`
}

type pageKey struct {
	directory string
	offset    int
}

type cachedPage struct {
	Page
	checked time.Time
}

type Native struct {
	client *driver.Pan115Client
	limit  *rate.Limiter
	gate   chan struct{}
	mu     sync.Mutex
	cache  map[pageKey]cachedPage
}

func OpenNative(filename string) (*Native, error) {
	data, err := os.ReadFile(filename)
	if err != nil || len(data) > 64<<10 {
		return nil, Unavailable
	}
	var cfg struct {
		Default string                             `toml:"default_profile"`
		Entries map[string]struct{ Cookie string } `toml:"profiles"`
	}
	if _, err := toml.Decode(string(data), &cfg); err != nil {
		return nil, Unavailable
	}
	if cfg.Default == "" {
		cfg.Default = "main"
	}
	var cred driver.Credential
	if err := cred.FromCookie(cfg.Entries[cfg.Default].Cookie); err != nil {
		return nil, Unavailable
	}
	client := driver.New().SetHttpClient(&http.Client{Timeout: 12 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).ImportCredential(&cred)
	client.Client.SetRetryCount(0)
	return &Native{client: client, limit: rate.NewLimiter(rate.Every(500*time.Millisecond), 1), gate: make(chan struct{}, 1), cache: make(map[pageKey]cachedPage)}, nil
}

func (n *Native) acquire(ctx context.Context) (func(), error) {
	select {
	case n.gate <- struct{}{}:
		return func() { <-n.gate }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (n *Native) dirID(ctx context.Context, directory string) (string, error) {
	if err := n.limit.Wait(ctx); err != nil {
		return "", err
	}
	if directory == "/" {
		return "0", nil
	}
	var data driver.APIGetDirIDResp
	resp, err := n.client.NewRequest().SetContext(ctx).ForceContentType("application/json").SetQueryParam("path", strings.TrimPrefix(directory, "/")).SetResult(&data).Get(driver.ApiDirName2CID)
	if err := driver.CheckErr(err, &data, resp); err != nil {
		return "", Unavailable
	}
	id := string(data.CategoryID)
	if !cloudID.MatchString(id) || id == "0" {
		return "", Missing
	}
	return id, nil
}

func (n *Native) listID(ctx context.Context, id string, offset int) (Page, error) {
	if err := n.limit.Wait(ctx); err != nil {
		return Page{}, err
	}
	result, err := driver.GetFiles(n.client.NewRequest().SetContext(ctx).ForceContentType("application/json"), id,
		driver.WithApiURL(driver.ApiFileList), driver.WithLimit(PageSize), driver.WithOffset(int64(offset)))
	if err != nil || result == nil || string(result.CategoryID) != id || result.Offset != offset || result.Count < 0 || len(result.Files) > PageSize || (len(result.Files) == 0 && offset < result.Count) {
		return Page{}, Unavailable
	}
	page := Page{Items: []Item{}, CID: id, Offset: offset}
	for _, raw := range result.Files {
		file := (&driver.File{}).From(&raw)
		if file.ParentID != id || !cloudID.MatchString(file.FileID) || !ValidName(file.Name) {
			return Page{}, Unavailable
		}
		page.Items = append(page.Items, Item{ID: file.FileID, Name: file.Name, Directory: file.IsDirectory, Size: file.Size})
	}
	if next := offset + len(result.Files); next < result.Count {
		page.Next = next
	}
	return page, nil
}

func (n *Native) List(ctx context.Context, directory string, offset int, refresh bool) (Page, error) {
	key := pageKey{directory, offset}
	requested := time.Now()
	release, err := n.acquire(ctx)
	if err != nil {
		return Page{}, err
	}
	defer release()
	n.mu.Lock()
	cached, ok := n.cache[key]
	n.mu.Unlock()
	if ok && ((!refresh && time.Since(cached.checked) < 20*time.Second) || (refresh && (cached.checked.After(requested) || time.Since(cached.checked) < 2*time.Second))) {
		return cached.Page, nil
	}
	id, err := n.dirID(ctx, directory)
	if err != nil {
		return Page{}, err
	}
	page, err := n.listID(ctx, id, offset)
	if err != nil {
		return Page{}, err
	}
	n.mu.Lock()
	if len(n.cache) >= 256 {
		clear(n.cache)
	}
	n.cache[key] = cachedPage{page, time.Now()}
	n.mu.Unlock()
	return page, nil
}

func (n *Native) Invalidate(directory string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for key := range n.cache {
		if key.directory == directory || strings.HasPrefix(key.directory, directory+"/") {
			delete(n.cache, key)
		}
	}
}

func ValidName(s string) bool {
	return s != "" && s != "." && s != ".." && len(s) <= 512 && utf8.ValidString(s) && !strings.ContainsAny(s, "/\\") && !strings.ContainsFunc(s, unicode.IsControl)
}

// EnsureChild uses an exact parent listing to distinguish an absent directory
// from an authentication or protocol error. It never adopts a file as a folder.
func (n *Native) EnsureChild(ctx context.Context, parent, name string) (string, error) {
	if !ValidName(name) {
		return "", Missing
	}
	release, err := n.acquire(ctx)
	if err != nil {
		return "", err
	}
	defer release()
	parentID, err := n.dirID(ctx, parent)
	if err != nil {
		return "", err
	}
	for offset := 0; ; {
		page, err := n.listID(ctx, parentID, offset)
		if err != nil {
			return "", err
		}
		for _, file := range page.Items {
			if file.Name == name {
				if !file.Directory {
					return "", Unavailable
				}
				return file.ID, nil
			}
		}
		if page.Next == 0 {
			break
		}
		if page.Next <= offset || page.Next >= 10000 {
			return "", Unavailable
		}
		offset = page.Next
	}
	if err := n.limit.Wait(ctx); err != nil {
		return "", err
	}
	id, err := n.client.Mkdir(parentID, name)
	if err != nil || !cloudID.MatchString(id) || id == "0" {
		return "", Unavailable
	}
	n.Invalidate(path.Join(parent, name))
	n.Invalidate(parent)
	return id, nil
}

func (n *Native) Start(ctx context.Context, source, directoryID string) (string, error) {
	if !cloudID.MatchString(directoryID) || directoryID == "0" {
		return "", Unavailable
	}
	release, err := n.acquire(ctx)
	if err != nil {
		return "", err
	}
	defer release()
	if err := n.limit.Wait(ctx); err != nil {
		return "", err
	}
	hashes, err := n.client.AddOfflineTaskURIs([]string{source}, directoryID)
	if err != nil || len(hashes) != 1 || hashes[0] == "" || len(hashes[0]) > 128 {
		return "", Unavailable
	}
	return strings.ToLower(hashes[0]), nil
}

func (n *Native) Tasks(ctx context.Context) ([]*driver.OfflineTask, error) {
	release, err := n.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	var list []*driver.OfflineTask
	for number := int64(1); number <= 3; number++ {
		if err := n.limit.Wait(ctx); err != nil {
			return nil, err
		}
		var data driver.OfflineTaskResp
		resp, err := n.client.NewRequest().SetContext(ctx).SetResult(&data).ForceContentType("application/json;charset=UTF-8").SetQueryParam("page", strconv.FormatInt(number, 10)).Get(driver.ApiListOfflineUrl)
		if err := driver.CheckErr(err, &data, resp); err != nil {
			return nil, Unavailable
		}
		list = append(list, data.Tasks...)
		if number >= data.PageCount {
			break
		}
	}
	return list, nil
}
