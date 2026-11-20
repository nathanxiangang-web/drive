package site

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestPlaybackCacheRefreshAndIsolation(t *testing.T) {
	var cache playbackCache
	key := playbackKey{owner: "one", directory: "/CloudSite/video", id: "42", agent: "browser-a"}
	var calls int
	get := func(context.Context) (string, error) {
		calls++
		return fmt.Sprintf("https://cdn.invalid/video?signature=%d", calls), nil
	}
	ctx := context.Background()
	first, status, err := cache.resolve(ctx, key, "", get)
	if err != nil || status != "MISS" {
		t.Fatalf("initial resolution %s %v", status, err)
	}
	next, status, err := cache.resolve(ctx, key, "", get)
	if err != nil || status != "HIT" || first != next || calls != 1 {
		t.Fatal("repeat playback did not reuse the link")
	}
	next, status, err = cache.resolve(ctx, key, "retry-one", get)
	if err != nil || status != "REFRESH" || first == next || calls != 2 {
		t.Fatal("recovery did not obtain a fresh link")
	}
	got, status, err := cache.resolve(ctx, key, "retry-one", get)
	if err != nil || status != "HIT" || got != next || calls != 2 {
		t.Fatal("the same recovery request resolved twice")
	}
	for _, change := range []func(*playbackKey){
		func(k *playbackKey) { k.owner = "two" },
		func(k *playbackKey) { k.agent = "browser-b" },
		func(k *playbackKey) { k.id = "43" },
		func(k *playbackKey) { k.source = 1 },
	} {
		other := key
		change(&other)
		got, status, err := cache.resolve(ctx, other, "", get)
		if err != nil || status != "MISS" || got == next {
			t.Fatal("a different account, browser, file or source shared the grant")
		}
	}
}

func TestPlaybackCacheRespectsProviderExpiry(t *testing.T) {
	now := time.Unix(1800000000, 0)
	for _, tc := range []struct {
		query string
		life  time.Duration
	}{{"", 30 * time.Second}, {"?t=1800000030", 15 * time.Second}, {"?t=1800003600", time.Minute}} {
		if got := playbackExpiry("https://cdn.invalid/video"+tc.query, now); !got.Equal(now.Add(tc.life)) {
			t.Errorf("expiry for %q: %s", tc.query, got.Sub(now))
		}
	}
}

func TestPlaybackCacheSharesOneResolution(t *testing.T) {
	var cache playbackCache
	key := playbackKey{id: "42"}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var calls atomic.Int32
	started, stop, complete := make(chan struct{}), make(chan struct{}), make(chan struct{})
	get := func(ctx context.Context) (string, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-stop:
			return "https://cdn.invalid/video", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	go func() {
		defer close(complete)
		_, _, _ = cache.resolve(ctx, key, "", get)
	}()
	<-started
	cancelled, stopWaiting := context.WithCancel(ctx)
	stopWaiting()
	_, status, _ := cache.resolve(cancelled, key, "", get)
	close(stop)
	<-complete
	if status != "JOIN" || calls.Load() != 1 {
		t.Fatal("concurrent playback started a duplicate resolution")
	}
}
