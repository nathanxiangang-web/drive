package cloud

import (
	"context"
	"path"
	"strconv"
	"strings"

	"github.com/SheltonZhu/115driver/pkg/driver"
)

const SearchSize = 12

type Match struct {
	Item
	ParentID string
}

func (n *Native) Search(ctx context.Context, rootID, query string, offset int) ([]Match, int, error) {
	if !cloudID.MatchString(rootID) || rootID == "0" {
		return nil, 0, Missing
	}
	release, err := n.acquire(ctx)
	if err != nil {
		return nil, 0, err
	}
	defer release()
	if err := n.limit.Wait(ctx); err != nil {
		return nil, 0, err
	}
	var data driver.FileListResp
	params := map[string]string{
		"aid": "7", "cid": rootID, "format": "json", "offset": strconv.Itoa(offset),
		"limit": strconv.Itoa(SearchSize), "search_value": query, "count_folders": "1",
		"o": "file_name", "asc": "1",
	}
	resp, err := n.client.NewRequest().SetContext(ctx).SetQueryParams(params).
		SetResult(&data).ForceContentType("application/json").Get(driver.ApiFileSearch)
	if driver.CheckErr(err, &data, resp) != nil || data.Count < 0 || data.Offset != offset || len(data.Files) > SearchSize {
		return nil, 0, Unavailable
	}
	matches := make([]Match, 0, len(data.Files))
	for _, row := range data.Files {
		file := (&driver.File{}).From(&row)
		if ValidName(file.Name) && cloudID.MatchString(file.FileID) && cloudID.MatchString(file.ParentID) &&
			strings.Contains(strings.ToLower(file.Name), strings.ToLower(query)) {
			matches = append(matches, Match{Item: Item{ID: file.FileID, Name: file.Name, Directory: file.IsDirectory, Size: file.Size, Modified: validTime(file.UpdateTime)}, ParentID: file.ParentID})
		}
	}
	next := offset + len(data.Files)
	if next >= data.Count || next == offset {
		next = 0
	}
	return matches, next, nil
}

type breadcrumb struct {
	ID   driver.IntString `json:"cid"`
	Name string           `json:"name"`
}

// RelativeParent proves the ancestry of a search hit independently of the search filter.
// The provider may return account-wide matches even when a directory is specified.
func (n *Native) RelativeParent(ctx context.Context, rootID, parentID string) (string, error) {
	if rootID == parentID {
		return "", nil
	}
	if !cloudID.MatchString(parentID) || parentID == "0" {
		return "", Missing
	}
	release, err := n.acquire(ctx)
	if err != nil {
		return "", err
	}
	defer release()
	if err := n.limit.Wait(ctx); err != nil {
		return "", err
	}
	var data struct {
		driver.FileListResp
		Path []breadcrumb `json:"path"`
	}
	params := map[string]string{"cid": parentID, "aid": "1", "limit": "1", "offset": "0", "show_dir": "1", "format": "json"}
	resp, err := n.client.NewRequest().SetContext(ctx).SetQueryParams(params).
		SetResult(&data).ForceContentType("application/json").Get(driver.ApiFileList)
	if driver.CheckErr(err, &data, resp) != nil {
		return "", Unavailable
	}
	if string(data.CategoryID) != parentID {
		return "", Missing
	}
	return relativeParent(data.Path, rootID, parentID)
}

func relativeParent(chain []breadcrumb, rootID, parentID string) (string, error) {
	if len(chain) == 0 || string(chain[len(chain)-1].ID) != parentID {
		return "", Missing
	}
	for index, folder := range chain {
		if string(folder.ID) != rootID {
			continue
		}
		names := make([]string, 0, len(chain)-index-1)
		for _, child := range chain[index+1:] {
			if !ValidName(child.Name) {
				return "", Missing
			}
			names = append(names, child.Name)
		}
		return path.Join(names...), nil
	}
	return "", Missing
}

// Child checks the exact child name using the authenticated parent's listing.
func (n *Native) Child(ctx context.Context, parent, name string, refresh bool) (string, error) {
	for offset := 0; ; {
		page, err := n.List(ctx, parent, offset, refresh)
		if err != nil {
			return "", err
		}
		for _, item := range page.Items {
			if item.Name == name && item.Directory {
				return item.ID, nil
			}
		}
		if page.Next <= offset {
			return "", Missing
		}
		offset = page.Next
	}
}
