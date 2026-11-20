package site

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/nathanxiangang-web/drive/internal/cloud"
	"github.com/nathanxiangang-web/drive/internal/store"
)

func TestPublicPlaybackReferenceUsesCurrentAccountAndScope(t *testing.T) {
	s := Site{PublicRoot: "/CloudSite", PrivateRoot: "/CloudSite/DriveSpace", LegacyRoot: "/CloudSite/DriveSpaces", searchKey: []byte("test-only playback key")}
	a := store.Account{ID: "member"}
	ref := searchRef{Account: a.ID, Area: "public", Dir: "videos", Parent: "20",
		Item: cloud.Item{ID: "42", Name: "fixture.mp4"}, Expires: time.Now().Add(time.Minute).Unix()}
	q := url.Values{"play": {"1"}, "ref": {s.signSearch(ref)}}
	got, ok := s.usePlaybackRef(context.Background(), a, q)
	if !ok || got.Item.ID != ref.Item.ID || got.Base != "/CloudSite/videos" || got.Parent != ref.Parent {
		t.Fatal("public playback reference did not keep the authorized file path")
	}
	if _, ok := s.usePlaybackRef(context.Background(), store.Account{ID: "other"}, q); ok {
		t.Fatal("accepted another account's playback reference")
	}
	for _, dir := range []string{"DriveSpace/alice", "../private"} {
		ref.Dir = dir
		q.Set("ref", s.signSearch(ref))
		if _, ok := s.usePlaybackRef(context.Background(), a, q); ok {
			t.Fatalf("accepted unauthorized directory %q", dir)
		}
	}
}
