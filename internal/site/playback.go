package site

import (
	"context"
	"net/url"
	"path"
	"strings"

	"github.com/nathanxiangang-web/drive/internal/store"
)

func mediaFile(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".mp4", ".webm", ".mov", ".m4v", ".mkv", ".avi", ".mp3", ".m4a", ".aac", ".ogg", ".wav", ".flac":
		return true
	default:
		return false
	}
}

// usePlaybackRef avoids a cold parent listing for a public video. The signed
// entry already passed the public directory policy when it was listed. On a
// cold directory the 115 file ID, parent, name and size are checked directly.
func (s *Site) usePlaybackRef(ctx context.Context, a store.Account, q url.Values) (fileSelection, bool) {
	if q.Get("play") != "1" {
		return fileSelection{}, false
	}
	token := q.Get("play_ref")
	if token == "" {
		token = q.Get("ref")
	}
	if token == "" {
		return fileSelection{}, false
	}
	ref, err := s.parseSearch(token, a.ID)
	if err != nil || (ref.Area != "public" && ref.Area != "") || ref.Item.Directory ||
		!mediaFile(ref.Item.Name) || !s.searchable("public", ref.Dir, ref.Item) {
		return fileSelection{}, false
	}
	base, err := s.directory(ctx, a, "public", ref.Dir)
	if err != nil {
		return fileSelection{}, false
	}
	return fileSelection{Item: ref.Item, Area: "public", Dir: ref.Dir, Base: base, Parent: ref.Parent}, true
}

func (s *Site) checkedFileTarget(ctx context.Context, selected fileSelection, userAgent string) (string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type resolution struct {
		url string
		err error
	}
	link := make(chan resolution, 1)
	go func() {
		target, err := s.fileTarget(ctx, selected.Base, selected.Item, userAgent)
		link <- resolution{target, err}
	}()
	if err := s.Drive.CheckFile(ctx, selected.Item, selected.Parent); err != nil {
		return "", err
	}
	resolved := <-link
	return resolved.url, resolved.err
}

func (s *Site) publicPlaybackTarget(ctx context.Context, selected fileSelection, offset int, userAgent string) (string, error) {
	page, ok := s.Drive.CachedList(selected.Base, offset)
	if ok && page.CID == selected.Parent {
		for _, item := range page.Items {
			if !item.Directory && item.ID == selected.Item.ID && item.Name == selected.Item.Name && item.Size == selected.Item.Size {
				return s.fileTarget(ctx, selected.Base, selected.Item, userAgent)
			}
		}
	}
	return s.checkedFileTarget(ctx, selected, userAgent)
}
