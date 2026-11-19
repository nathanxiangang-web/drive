package site

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/nathanxiangang-web/drive/internal/cloud"
	"github.com/nathanxiangang-web/drive/internal/store"
)

var invalidSearch = errors.New("请输入 1 到 100 字的文件名关键词")

type searchRef struct {
	Account, Area, Dir, Parent string
	Item                       cloud.Item
	Expires                    int64
}

func (s *Site) signSearch(ref searchRef) string {
	body, _ := json.Marshal(ref)
	margin := hmac.New(sha256.New, s.searchKey)
	margin.Write(body)
	return base64.RawURLEncoding.EncodeToString(body) + "." + base64.RawURLEncoding.EncodeToString(margin.Sum(nil))
}

func (s *Site) parseSearch(token, owner string) (searchRef, error) {
	var ref searchRef
	if len(token) > 8192 {
		return ref, cloud.Missing
	}
	payload, mac, _ := strings.Cut(token, ".")
	body, err := base64.RawURLEncoding.DecodeString(payload)
	signature, sigErr := base64.RawURLEncoding.DecodeString(mac)
	margin := hmac.New(sha256.New, s.searchKey)
	margin.Write(body)
	if err != nil || sigErr != nil || !hmac.Equal(margin.Sum(nil), signature) ||
		json.Unmarshal(body, &ref) != nil || ref.Account != owner || ref.Expires <= time.Now().Unix() {
		return searchRef{}, cloud.Missing
	}
	return ref, nil
}

func (s *Site) searchPage(ctx context.Context, a store.Account, area, relative, keyword string, offset int) (entryPage, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" || !utf8.ValidString(keyword) || utf8.RuneCountInString(keyword) > 100 || strings.ContainsFunc(keyword, unicode.IsControl) {
		return entryPage{}, invalidSearch
	}
	if area != "private" || relative != "" {
		base, err := s.directory(ctx, a, area, relative)
		if err != nil {
			return entryPage{}, err
		}
		return s.searchDirectory(ctx, a, area, relative, base, keyword, offset)
	}
	if offset < sourceStride {
		base, err := s.namedSpace(ctx, a)
		switch {
		case err == nil:
			page, err := s.searchDirectory(ctx, a, area, "", base, keyword, offset)
			if err != nil || page.Next != 0 || a.FolderID == "" {
				return page, err
			}
			if len(page.Items) != 0 {
				page.Next = sourceStride
				return page, nil
			}
		case !errors.Is(err, cloud.Missing):
			return entryPage{}, err
		}
		offset = sourceStride
	}
	base, err := s.legacySpace(ctx, a)
	if errors.Is(err, cloud.Missing) {
		return entryPage{Items: []entry{}}, nil
	}
	if err != nil {
		return entryPage{}, err
	}
	page, err := s.searchDirectory(ctx, a, area, legacyPrefix, base, keyword, offset-sourceStride)
	if page.Next != 0 {
		page.Next += sourceStride
	}
	return page, err
}

func (s *Site) searchable(area, directory string, item cloud.Item) bool {
	if area == "private" {
		return directory != legacyPrefix || !isTaskFolder(item)
	}
	first, _, _ := strings.Cut(path.Join(directory, item.Name), "/")
	return !s.publicRestricted(first)
}

func (s *Site) searchDirectory(ctx context.Context, a store.Account, area, relative, base, keyword string, offset int) (entryPage, error) {
	browse, err := s.Drive.List(ctx, base, 0, false)
	if err != nil {
		return entryPage{}, err
	}
	hits, next, err := s.Drive.Search(ctx, browse.CID, keyword, offset)
	if err != nil {
		return entryPage{}, err
	}
	page := entryPage{Items: []entry{}, Next: next}
	paths := make(map[string]string)
	for _, hit := range hits {
		parent, ok := paths[hit.ParentID]
		if !ok {
			parent, err = s.Drive.RelativeParent(ctx, browse.CID, hit.ParentID)
			if errors.Is(err, cloud.Missing) {
				continue
			}
			if err != nil {
				return entryPage{}, err
			}
			paths[hit.ParentID] = parent
		}
		dir := path.Join(relative, parent)
		if !s.searchable(area, dir, hit.Item) {
			continue
		}
		item := entry{Item: hit.Item, Dir: dir}
		if !item.Directory {
			item.Ref = s.signSearch(searchRef{a.ID, area, dir, hit.ParentID, item.Item, time.Now().Add(10 * time.Minute).Unix()})
		}
		page.Items = append(page.Items, item)
	}
	return page, nil
}
