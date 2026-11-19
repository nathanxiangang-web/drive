package site

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nathanxiangang-web/drive/internal/cloud"
)

func TestSearchScopeAndReference(t *testing.T) {
	s := Site{PrivateRoot: "/CloudSite/DriveSpace", LegacyRoot: "/CloudSite/DriveSpaces", searchKey: []byte("fixture")}
	item := cloud.Item{Name: "file.mp4"}
	for _, dir := range []string{"DriveSpace/admin", "drivespaces/user-a", "云下载/other"} {
		if s.searchable("public", dir, item) {
			t.Fatalf("public search leaked an entry from %s", dir)
		}
	}
	if s.searchable("private", legacyPrefix, cloud.Item{Name: "task-abcdefabcdef", Directory: true}) {
		t.Fatal("legacy task wrapper appeared in results")
	}
	token := s.signSearch(searchRef{Account: "alice", Item: item, Expires: time.Now().Add(time.Minute).Unix()})
	if _, err := s.parseSearch(token, "alice"); err != nil {
		t.Fatal(err)
	}
	for _, trial := range []struct{ token, user string }{{token, "bob"}, {"A" + strings.TrimPrefix(token, "A"), "alice"}} {
		if _, err := s.parseSearch(trial.token, trial.user); !errors.Is(err, cloud.Missing) {
			t.Fatal("unauthorized search reference accepted")
		}
	}
}
