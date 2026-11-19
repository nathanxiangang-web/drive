package download

import (
	"context"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"net"
	"net/url"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/nathanxiangang-web/drive/internal/cloud"
	"github.com/nathanxiangang-web/drive/internal/store"
)

var InvalidSource = errors.New("请输入有效的 HTTP、HTTPS、ED2K 或 BTIH 磁力链接")

func Prepare(raw string) (execution, hash string, err error) {
	if raw == "" || len(raw) > 65536 || !utf8.ValidString(raw) || strings.TrimSpace(raw) != raw || strings.ContainsFunc(raw, unicode.IsControl) {
		return "", "", InvalidSource
	}
	scheme, _, _ := strings.Cut(raw, ":")
	switch strings.ToLower(scheme) {
	case "http", "https", "ed2k":
		if len(raw) > 4096 {
			return "", "", InvalidSource
		}
		if strings.EqualFold(scheme, "ed2k") {
			if !strings.HasPrefix(strings.ToLower(raw), "ed2k://|file|") {
				return "", "", InvalidSource
			}
			return raw, "", nil
		}
		remote, e := url.Parse(raw)
		if e != nil || remote.Hostname() == "" || remote.User != nil || remote.Opaque != "" {
			return "", "", InvalidSource
		}
		host := strings.ToLower(remote.Hostname())
		if !strings.Contains(host, ".") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
			return "", "", InvalidSource
		}
		if ip := net.ParseIP(host); ip != nil && (!ip.IsGlobalUnicast() || ip.IsPrivate()) {
			return "", "", InvalidSource
		}
		return raw, "", nil
	case "magnet":
		return magnet(raw)
	default:
		return "", "", InvalidSource
	}
}

func magnet(raw string) (string, string, error) {
	parsed, e := url.Parse(strings.ReplaceAll(raw, "&amp;", "&"))
	if e != nil || parsed.Host != "" || parsed.Path != "" || parsed.Opaque != "" || strings.Contains(raw, "#") || strings.Count(parsed.RawQuery, "&") >= 512 {
		return "", "", InvalidSource
	}
	items, e := url.ParseQuery(parsed.RawQuery)
	if e != nil {
		return "", "", InvalidSource
	}
	var hash, name string
	var seenName bool
	for key, values := range items {
		for _, value := range values {
			if !utf8.ValidString(value) || strings.ContainsFunc(value, unicode.IsControl) {
				return "", "", InvalidSource
			}
			switch strings.ToLower(key) {
			case "so":
				return "", "", errors.New("暂不支持磁力链接的选定文件下载")
			case "xt":
				const prefix = "urn:btih:"
				if !strings.HasPrefix(strings.ToLower(value), prefix) {
					return "", "", errors.New("只支持 BTIH 磁力链接")
				}
				h := value[len(prefix):]
				var decoded []byte
				var err error
				if len(h) == 40 {
					decoded, err = hex.DecodeString(h)
				} else if len(h) == 32 {
					decoded, err = base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(h))
				}
				if err != nil || len(decoded) != 20 {
					return "", "", InvalidSource
				}
				next := hex.EncodeToString(decoded)
				if hash != "" && hash != next {
					return "", "", InvalidSource
				}
				hash = next
			case "dn":
				if seenName && name != value {
					return "", "", InvalidSource
				}
				name = value
				seenName = true
			}
		}
	}
	if hash == "" {
		return "", "", errors.New("磁力链接缺少有效的 BTIH")
	}
	result := "magnet:?xt=urn:btih:" + hash
	if name != "" {
		result += "&dn=" + strings.ReplaceAll(url.QueryEscape(name), "+", "%20")
	}
	if len(result) > 4096 {
		return "", "", InvalidSource
	}
	return result, hash, nil
}

type Worker struct {
	Store       *store.Store
	Drive       *cloud.Native
	PrivateRoot string
}

func (w Worker) Run(ctx context.Context) {
	dispatch := time.NewTicker(4 * time.Second)
	poll := time.NewTicker(25 * time.Second)
	defer dispatch.Stop()
	defer poll.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-dispatch.C:
			w.dispatch(ctx)
		case <-poll.C:
			w.poll(ctx)
		}
	}
}

func (w Worker) dispatch(ctx context.Context) {
	taskCtx, cancel := context.WithTimeout(ctx, 50*time.Second)
	defer cancel()
	job, err := w.Store.Claim(taskCtx)
	if err != nil {
		return
	}
	account, err := w.Store.User(taskCtx, job.Owner)
	if err != nil {
		w.finish(ctx, job.ID, "FAILED", "", "")
		return
	}
	if _, err = w.Drive.EnsureChild(taskCtx, path.Dir(w.PrivateRoot), path.Base(w.PrivateRoot)); err != nil {
		w.finish(ctx, job.ID, "DIRECTORY_UNAVAILABLE", "", "")
		return
	}
	ownerFolder, err := w.Drive.EnsureChild(taskCtx, w.PrivateRoot, account.FolderName())
	if err != nil || w.Store.BindFolder(taskCtx, job.Owner, ownerFolder) != nil {
		w.finish(ctx, job.ID, "DIRECTORY_UNAVAILABLE", "", "")
		return
	}
	directory := path.Join(w.PrivateRoot, account.FolderName())
	resultDir, err := w.Drive.EnsureChild(taskCtx, directory, "task-"+job.ID[:12])
	if err != nil {
		w.finish(ctx, job.ID, "DIRECTORY_UNAVAILABLE", "", "")
		return
	}
	_, expected, e := Prepare(job.Source)
	if e != nil || w.Store.RecordTarget(taskCtx, job.ID, resultDir, expected) != nil {
		w.finish(ctx, job.ID, "FAILED", "", "")
		return
	}
	providerHash, err := w.Drive.Start(taskCtx, job.Source, resultDir)
	switch {
	case errors.Is(err, cloud.Authorize):
		w.finish(ctx, job.ID, "AUTH_REQUIRED", "", "")
		return
	case errors.Is(err, cloud.Rejected):
		w.finish(ctx, job.ID, "REJECTED", "", "")
		return
	case err != nil:
		w.finish(ctx, job.ID, "UNCERTAIN", expected, "")
		return
	}
	w.finish(ctx, job.ID, "RUNNING", providerHash, "")
}

func (w Worker) finish(ctx context.Context, id, status, hash, name string) {
	update, cancel := context.WithTimeout(context.WithoutCancel(ctx), 4*time.Second)
	defer cancel()
	_ = w.Store.Update(update, id, status, hash, name)
}

func (w Worker) poll(ctx context.Context) {
	pollCtx, cancel := context.WithTimeout(ctx, 24*time.Second)
	defer cancel()
	jobs, err := w.Store.ActiveJobs(pollCtx)
	if err != nil || len(jobs) == 0 {
		return
	}
	known, err := w.Drive.Tasks(pollCtx)
	if err != nil {
		return
	}
	for _, job := range jobs {
		for _, task := range known {
			if task == nil || task.InfoHash == "" || (job.ProviderHash != "" && !strings.EqualFold(task.InfoHash, job.ProviderHash)) || task.DirId != job.DirectoryID || task.AddTime < job.Created.Add(-time.Minute).Unix() {
				continue
			}
			switch task.Status {
			case -1:
				w.finish(ctx, job.ID, "FAILED", "", "")
			case 0, 1:
				if job.Status != "RUNNING" {
					w.finish(ctx, job.ID, "RUNNING", task.InfoHash, task.Name)
				}
			case 2:
				w.verify(pollCtx, job, task.FileId, task.Name)
			}
			break
		}
	}
}

func (w Worker) verify(ctx context.Context, job store.Job, fileID, name string) {
	if job.Status != "VERIFYING" {
		w.finish(ctx, job.ID, "VERIFYING", "", name)
	}
	account, err := w.Store.User(ctx, job.Owner)
	if err != nil {
		return
	}
	ownerPath := path.Join(w.PrivateRoot, account.FolderName())
	owner, err := w.Drive.List(ctx, ownerPath, 0, false)
	if err != nil || owner.CID != account.FolderID {
		return
	}
	dir := path.Join(ownerPath, "task-"+job.ID[:12])
	for offset := 0; offset < 150; {
		page, err := w.Drive.List(ctx, dir, offset, true)
		if err != nil || page.CID != job.DirectoryID {
			return
		}
		var nameMatch int
		for _, item := range page.Items {
			if fileID != "" && fileID != "0" && item.ID == fileID {
				w.Drive.Invalidate(ownerPath)
				w.finish(ctx, job.ID, "READY", "", item.Name)
				return
			}
			if (fileID == "" || fileID == "0") && item.Name == name {
				nameMatch++
			}
		}
		// Each job has its own empty destination. If the provider supplies no
		// result ID, require a single matching child in the complete first page.
		if offset == 0 && page.Next == 0 && nameMatch == 1 {
			w.Drive.Invalidate(ownerPath)
			w.finish(ctx, job.ID, "READY", "", name)
			return
		}
		if page.Next <= offset {
			return
		}
		offset = page.Next
	}
}
