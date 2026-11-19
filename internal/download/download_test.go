package download

import (
	"github.com/nathanxiangang-web/drive/internal/cloud"
	"github.com/nathanxiangang-web/drive/internal/store"
	"testing"
	"time"
)

func TestSourceKeepsIdentityAndRejectsSelectiveDownloads(t *testing.T) {
	const hash = "0123456789abcdef0123456789abcdef01234567"
	base := "magnet:?xt=urn:btih:" + hash
	got, id, err := Prepare(base + "&amp;dn=sample&amp;tr=udp%3A%2F%2Ftracker.invalid")
	if err != nil || id != hash || got != base+"&dn=sample" {
		t.Fatalf("normalized input: %q %q %v", got, id, err)
	}
	for _, s := range []string{base + "&amp;%73o=0", base + "&xt=urn:btih:1111111111111111111111111111111111111111", "magnet:?dn=no-hash", "https://127.0.0.1/file"} {
		if _, _, err := Prepare(s); err == nil {
			t.Errorf("accepted %q", s)
		}
	}
}

func TestCompletedNameCannotMatchPreviouslyDownloadedFile(t *testing.T) {
	submitted := time.Unix(100, 500)
	job := store.Job{Created: submitted, NamedDestination: true}
	item := cloud.Item{Name: "example.mp4", Created: time.Unix(99, 0)}
	if matchesCompletedName(job, item, item.Name) {
		t.Fatal("old same-name file was accepted")
	}
	item.Created = time.Unix(100, 0)
	if !matchesCompletedName(job, item, item.Name) {
		t.Fatal("new same-name file was rejected")
	}
}
