package download

import (
	"context"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"math"
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

var InvalidSource = errors.New("请输入 HTTP、HTTPS、ED2K 或磁力链接")

// Prepare preserves the source accepted by the provider. A BTIH is only a
// recovery hint and is not required for submission.
func Prepare(raw string) (execution, hash string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || !utf8.ValidString(raw) || strings.ContainsFunc(raw, unicode.IsControl) {
		return "", "", InvalidSource
	}
	scheme, _, _ := strings.Cut(raw, ":")
	switch strings.ToLower(scheme) {
	case "magnet":
		raw = strings.ReplaceAll(raw, "&amp;", "&")
		return raw, recoveryHash(raw), nil
	case "ed2k":
		return raw, "", nil
	case "http", "https":
		remote, err := url.Parse(raw)
		if err != nil || remote.Hostname() == "" || remote.User != nil || remote.Opaque != "" {
			return "", "", InvalidSource
		}
		host := strings.ToLower(strings.TrimSuffix(remote.Hostname(), "."))
		if !strings.Contains(host, ".") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
			return "", "", InvalidSource
		}
		if ip := net.ParseIP(host); ip != nil && (!ip.IsGlobalUnicast() || ip.IsPrivate()) {
			return "", "", InvalidSource
		}
		return raw, "", nil
	default:
		return "", "", InvalidSource
	}
}

func recoveryHash(raw string) string {
	link, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	var hash string
	for key, values := range link.Query() {
		if !strings.EqualFold(key, "xt") {
			continue
		}
		for _, value := range values {
			value, ok := strings.CutPrefix(strings.ToLower(value), "urn:btih:")
			if !ok {
				continue
			}
			var decoded []byte
			switch len(value) {
			case 40:
				decoded, err = hex.DecodeString(value)
			case 32:
				decoded, err = base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(value))
			default:
				return ""
			}
			if err != nil || len(decoded) != 20 {
				return ""
			}
			candidate := hex.EncodeToString(decoded)
			if hash != "" && candidate != hash {
				return ""
			}
			hash = candidate
		}
	}
	return hash
}

type Worker struct {
	Store       *store.Store
	Drive       *cloud.Native
	PrivateRoot string
	LegacyRoot  string
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
	ownerFolder, err := w.Drive.EnsureChild(taskCtx, w.PrivateRoot, account.CloudFolderName())
	if err != nil || w.Store.BindNamedFolder(taskCtx, job.Owner, account.CloudFolderName(), ownerFolder) != nil {
		w.finish(ctx, job.ID, "DIRECTORY_UNAVAILABLE", "", "")
		return
	}
	resultDir := ownerFolder
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
	known, complete, err := w.Drive.Tasks(pollCtx)
	if err != nil {
		return
	}
	for _, job := range jobs {
		if job.NamedDestination && job.ProviderHash == "" {
			continue
		}
		found := false
		for _, task := range known {
			if task == nil || task.InfoHash == "" || (job.ProviderHash != "" && !strings.EqualFold(task.InfoHash, job.ProviderHash)) || task.DirId != job.DirectoryID || task.AddTime < job.Created.Add(-time.Minute).Unix() {
				continue
			}
			found = true
			progress := task.Percent
			if math.IsNaN(progress) || progress < 0 || progress > 100 {
				progress = -1
			}
			_ = w.Store.ObserveTask(pollCtx, job.ID, "LISTED", progress)
			switch task.Status {
			case -1:
				w.finish(ctx, job.ID, "FAILED", "", "")
			case 0, 1:
				if job.Status != "RUNNING" || (task.Name != "" && job.Name != task.Name) {
					w.finish(ctx, job.ID, "RUNNING", task.InfoHash, task.Name)
				}
			case 2:
				w.verify(pollCtx, job, task.FileId, task.Name)
			}
			break
		}
		if !found && complete && time.Since(job.Created) > time.Minute {
			_ = w.Store.ObserveTask(pollCtx, job.ID, "NOT_LISTED", -1)
		}
	}
}

func (w Worker) verify(ctx context.Context, job store.Job, fileID, name string) {
	if job.Status != "VERIFYING" {
		w.finish(ctx, job.ID, "VERIFYING", "", name)
	}
	account, err := w.Store.ResultOwner(ctx, job.Owner)
	if err != nil {
		return
	}
	ownerPath, folderID := path.Join(w.LegacyRoot, account.FolderName()), account.FolderID
	if job.NamedDestination {
		ownerPath, folderID = path.Join(w.PrivateRoot, account.CloudFolderName()), account.NamedFolderID
	}
	owner, err := w.Drive.List(ctx, ownerPath, 0, false)
	if err != nil || owner.CID != folderID {
		return
	}
	dir := ownerPath
	if !job.NamedDestination {
		dir = path.Join(ownerPath, "task-"+job.ID[:12])
	}
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
			if (fileID == "" || fileID == "0") && matchesCompletedName(job, item, name) {
				nameMatch++
			}
		}
		// Without a result ID, require one matching child in the complete page.
		// A shared user directory must also show that the child is new.
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

func matchesCompletedName(job store.Job, item cloud.Item, name string) bool {
	return item.Name == name && (!job.NamedDestination || (!item.Created.IsZero() && !item.Created.Before(job.Created.Truncate(time.Second))))
}
