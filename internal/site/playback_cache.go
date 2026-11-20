package site

import (
	"context"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/nathanxiangang-web/drive/internal/cloud"
)

type playbackKey struct {
	owner, directory, parent, id, name, agent string
	size, modified                            int64
	source                                    uint64
}

type cachedPlayback struct {
	target  string
	until   time.Time
	refresh string
}

type playbackFlight struct {
	done   chan struct{}
	target string
	err    error
}

type playbackCache struct {
	mu      sync.Mutex
	links   map[playbackKey]cachedPlayback
	flights map[playbackKey]*playbackFlight
}

// Keep signed redirects in memory for a short, absolute lifetime. The CDN
// URL's observed Unix expiration parameter takes priority when it is sooner.
func playbackExpiry(target string, now time.Time) time.Time {
	parsed, err := url.Parse(target)
	if err == nil {
		raw := parsed.Query().Get("t")
		if stamp, err := strconv.ParseInt(raw, 10, 64); err == nil && len(raw) == 10 {
			deadline := now.Add(time.Minute)
			if expiry := time.Unix(stamp, 0).Add(-15 * time.Second); expiry.Before(deadline) {
				return expiry
			}
			return deadline
		}
	}
	return now.Add(30 * time.Second)
}

func (c *playbackCache) resolve(ctx context.Context, key playbackKey, refresh string, get func(context.Context) (string, error)) (string, string, error) {
	c.mu.Lock()
	now := time.Now()
	entry, cached := c.links[key]
	if cached && (refresh == "" || refresh == entry.refresh) && now.Before(entry.until) {
		c.mu.Unlock()
		return entry.target, "HIT", nil
	}
	delete(c.links, key)
	call := c.flights[key]
	outcome := "JOIN"
	if call == nil {
		if len(c.flights) >= 64 {
			c.mu.Unlock()
			return "", "BUSY", cloud.Unavailable
		}
		if c.flights == nil {
			c.flights = make(map[playbackKey]*playbackFlight)
		}
		call = &playbackFlight{done: make(chan struct{})}
		c.flights[key] = call
		outcome = "MISS"
		if refresh != "" {
			outcome = "REFRESH"
		}
		go c.fill(context.WithoutCancel(ctx), key, refresh, call, get)
	}
	c.mu.Unlock()

	select {
	case <-ctx.Done():
		return "", outcome, ctx.Err()
	case <-call.done:
		return call.target, outcome, call.err
	}
}

func (c *playbackCache) fill(ctx context.Context, key playbackKey, refresh string, call *playbackFlight, get func(context.Context) (string, error)) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	call.target, call.err = get(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.flights, key)
	if call.err == nil {
		now := time.Now()
		until := playbackExpiry(call.target, now)
		if now.Before(until) {
			if c.links == nil {
				c.links = make(map[playbackKey]cachedPlayback)
			}
			if len(c.links) >= 256 {
				clear(c.links)
			}
			c.links[key] = cachedPlayback{call.target, until, refresh}
		}
	}
	close(call.done)
}
