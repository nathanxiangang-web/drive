package cloud

import (
	"context"
	"errors"

	"github.com/SheltonZhu/115driver/pkg/driver"
)

// CheckFile verifies the stable provider ID as well as its current location
// and identity. A replacement with the same name cannot inherit a share.
func (n *Native) CheckFile(ctx context.Context, expected Item, parent string) error {
	if expected.Directory || !cloudID.MatchString(expected.ID) || !cloudID.MatchString(parent) {
		return Missing
	}
	release, err := n.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	if err := n.limit.Wait(ctx); err != nil {
		return err
	}
	var data driver.GetFileInfoResponse
	resp, err := n.client.NewRequest().SetContext(ctx).SetQueryParam("file_id", expected.ID).
		ForceContentType("application/json").SetResult(&data).Get(driver.ApiFileInfo)
	if err := driver.CheckErr(err, &data, resp); err != nil {
		if errors.Is(err, driver.ErrNotExist) || errors.Is(err, driver.ErrDownloadFileNotExistOrHasDeleted) {
			return Missing
		}
		return Unavailable
	}
	if len(data.Files) != 1 || data.Files[0] == nil || !matchesFileInfo(data.Files[0], expected, parent) {
		return Missing
	}
	return nil
}

func matchesFileInfo(info *driver.FileInfo, file Item, parent string) bool {
	return info.FileID == file.ID && string(info.CategoryID) == parent &&
		info.Name == file.Name && int64(info.Size) == file.Size
}
