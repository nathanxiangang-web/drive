package cloud

import (
	"context"
	"testing"
	"time"
)

func TestCachedListDoesNotWaitBehindProviderOperation(t *testing.T) {
	const dir = "/public/videos"
	n := &Native{
		gate:  make(chan struct{}, 1),
		cache: map[pageKey]cachedPage{{dir, 0}: {Page: Page{CID: "123"}, checked: time.Now()}},
	}
	n.gate <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	page, err := n.List(ctx, dir, 0, false)
	if err != nil || page.CID != "123" {
		t.Fatalf("warm listing waited behind the provider gate: %v", err)
	}
}
