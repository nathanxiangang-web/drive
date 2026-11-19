package site

import (
	"context"
	"errors"
	"path"
	"regexp"
	"strings"

	"github.com/nathanxiangang-web/drive/internal/cloud"
	"github.com/nathanxiangang-web/drive/internal/store"
)

const legacyPrefix = "!legacy"
const sourceStride = 1_000_001

var taskFolderName = regexp.MustCompile(`^task-[a-f0-9]{12}$`)

func isTaskFolder(item cloud.Item) bool {
	return item.Directory && taskFolderName.MatchString(item.Name)
}

type entry struct {
	cloud.Item
	Dir    string `json:"dir"`
	Offset int    `json:"offset"`
	Ref    string `json:"ref,omitempty"`
}

type entryPage struct {
	Items  []entry `json:"items"`
	Offset int     `json:"offset"`
	Next   int     `json:"next"`
}

func (s *Site) namedSpace(ctx context.Context, a store.Account) (string, error) {
	if s.Drive == nil || !cloud.ValidName(a.CloudFolderName()) {
		return "", cloud.Missing
	}
	base := path.Join(s.PrivateRoot, a.CloudFolderName())
	if a.NamedFolderID == "" {
		if _, err := s.Drive.Child(ctx, s.PublicRoot, path.Base(s.PrivateRoot), false); err != nil {
			return "", err
		}
		id, err := s.Drive.Child(ctx, s.PrivateRoot, a.CloudFolderName(), false)
		if err != nil {
			return "", err
		}
		if err := s.Store.BindNamedFolder(ctx, a.ID, a.CloudFolderName(), id); err != nil {
			return "", err
		}
		a.NamedFolderID = id
	}
	page, err := s.Drive.List(ctx, base, 0, false)
	if err != nil {
		return "", err
	}
	if page.CID != a.NamedFolderID {
		return "", cloud.Missing
	}
	return base, nil
}

func (s *Site) legacySpace(ctx context.Context, a store.Account) (string, error) {
	if a.FolderID == "" {
		return "", cloud.Missing
	}
	base := path.Join(s.LegacyRoot, a.FolderName())
	page, err := s.Drive.List(ctx, base, 0, false)
	if err != nil {
		// A former provider path can vanish while the account still has old
		// job rows. Check the parent before treating the whole space as broken.
		if _, lookup := s.Drive.Child(ctx, s.PublicRoot, path.Base(s.LegacyRoot), false); errors.Is(lookup, cloud.Missing) {
			return "", cloud.Missing
		}
		if _, lookup := s.Drive.Child(ctx, s.LegacyRoot, a.FolderName(), false); errors.Is(lookup, cloud.Missing) {
			return "", cloud.Missing
		}
		return "", err
	}
	if page.CID != a.FolderID {
		return "", cloud.Missing
	}
	return base, nil
}

func (s *Site) privateDirectory(ctx context.Context, a store.Account, relative string) (string, error) {
	first, rest, _ := strings.Cut(relative, "/")
	if first == legacyPrefix {
		if rest == "" {
			return "", cloud.Missing
		}
		base, err := s.legacySpace(ctx, a)
		if err != nil {
			return "", err
		}
		return path.Join(base, rest), nil
	}
	base, err := s.namedSpace(ctx, a)
	if err != nil {
		return "", err
	}
	return path.Join(base, relative), nil
}

func (s *Site) listPage(ctx context.Context, area, relative, base string, offset int, refresh bool) (entryPage, error) {
	page, err := s.Drive.List(ctx, base, offset, refresh)
	if err != nil {
		return entryPage{}, err
	}
	return s.makePage(area, relative, page), nil
}

func (s *Site) makePage(area, relative string, page cloud.Page) entryPage {
	result := entryPage{Items: []entry{}, Next: page.Next}
	for _, item := range page.Items {
		if s.visible(area, relative, item) {
			result.Items = append(result.Items, entry{Item: item, Dir: relative, Offset: page.Offset})
		}
	}
	return result
}

// privateRoot lists the account's current folder and projects legacy task
// destinations as their contents. The originals stay at their provider paths.
func (s *Site) privateRoot(ctx context.Context, a store.Account, offset int, refresh bool) (entryPage, error) {
	var targets []store.LegacyTarget
	var err error
	if a.FolderID != "" {
		targets, err = s.Store.LegacyTargets(ctx, a.ID)
		if err != nil {
			return entryPage{}, err
		}
	}
	source, pageOffset := offset/sourceStride, offset%sourceStride
	if source == 0 {
		base, err := s.namedSpace(ctx, a)
		switch {
		case err == nil:
			page, err := s.listPage(ctx, "private", "", base, pageOffset, refresh)
			if err != nil {
				return entryPage{}, err
			}
			if page.Next != 0 || len(targets) == 0 {
				return page, nil
			}
			if len(page.Items) != 0 {
				page.Next = sourceStride
				return page, nil
			}
		case !errors.Is(err, cloud.Missing):
			return entryPage{}, err
		}
		source, pageOffset = 1, 0
	}
	if source > len(targets) {
		return entryPage{Items: []entry{}}, nil
	}
	base, err := s.legacySpace(ctx, a)
	if errors.Is(err, cloud.Missing) {
		return entryPage{Items: []entry{}}, nil
	}
	if err != nil {
		return entryPage{}, err
	}
	for ; source <= len(targets); source++ {
		target := targets[source-1]
		child, err := s.Drive.Child(ctx, base, target.Name, refresh)
		if errors.Is(err, cloud.Missing) || (err == nil && child != target.ID) {
			pageOffset = 0
			continue
		}
		if err != nil {
			return entryPage{}, err
		}
		page, err := s.Drive.List(ctx, path.Join(base, target.Name), pageOffset, refresh)
		if errors.Is(err, cloud.Missing) || (err == nil && page.CID != target.ID) {
			pageOffset = 0
			continue
		}
		if err != nil {
			return entryPage{}, err
		}
		projected := s.makePage("private", path.Join(legacyPrefix, target.Name), page)
		if page.Next != 0 {
			projected.Next += source * sourceStride
			return projected, nil
		}
		if len(projected.Items) != 0 {
			if source < len(targets) {
				projected.Next = (source + 1) * sourceStride
			}
			return projected, nil
		}
		pageOffset = 0
	}
	return entryPage{Items: []entry{}}, nil
}
