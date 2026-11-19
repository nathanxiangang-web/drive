package download

import (
	"github.com/nathanxiangang-web/drive/internal/cloud"
	"github.com/nathanxiangang-web/drive/internal/store"
	"strings"
	"testing"
	"time"
)

func TestSourcePassesCompleteMagnetThrough(t *testing.T) {
	const hash = "0123456789abcdef0123456789abcdef01234567"
	base := "magnet:?xt=urn:btih:" + hash
	params := "&amp;dn=sample&amp;so=0&amp;tr=udp%3A%2F%2Ftracker.invalid" + strings.Repeat("&amp;tr=https://tracker.invalid/announce", 2500)
	got, id, err := Prepare(base + params)
	if err != nil || id != hash || got != base+strings.ReplaceAll(params, "&amp;", "&") {
		t.Fatal("magnet parameters must reach the provider unchanged")
	}
	for _, value := range []string{"magnet:?dn=no-hash&so=0", "magnet:?xt=urn:btmh:1220&tr=http://tracker.invalid"} {
		if got, _, err := Prepare(value); err != nil || got != value {
			t.Fatal("the provider decides which magnet formats it supports")
		}
	}
	if _, _, err := Prepare("https://127.0.0.1/file"); err == nil {
		t.Fatal("loopback URL accepted")
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
