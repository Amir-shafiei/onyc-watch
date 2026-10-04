package main

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Providers only touch this cache. User preferences, alert latches and disk
// persistence remain owned by the bot event loop.
type MarketCache struct {
	mu      sync.RWMutex
	current Snapshot
	refresh []chan struct{}
}

func newMarketCache(seed Snapshot) *MarketCache { return &MarketCache{current: seed} }
func (c *MarketCache) snapshot() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s := c.current
	s.Yields = append([]YieldMarket(nil), s.Yields...)
	s.Borrows = append([]BorrowMarket(nil), s.Borrows...)
	s.Sources = append([]SourceStatus(nil), s.Sources...)
	return s
}
func (c *MarketCache) publish(name string, y []YieldMarket, b []BorrowMarket, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now().UTC()
	// Allocate new slices: previously rendered snapshots remain immutable.
	next := Snapshot{FetchedAt: now}
	for _, v := range c.current.Yields {
		if err != nil || yieldSource(v) != name {
			next.Yields = append(next.Yields, v)
		}
	}
	for _, v := range c.current.Borrows {
		if err != nil || v.Platform != name {
			next.Borrows = append(next.Borrows, v)
		}
	}
	for _, v := range c.current.Sources {
		if v.Name != name {
			next.Sources = append(next.Sources, v)
		}
	}
	st := SourceStatus{Name: name, OK: err == nil, FetchedAt: now}
	if err != nil {
		st.Error = err.Error()
	} else {
		next.Yields = append(next.Yields, y...)
		next.Borrows = append(next.Borrows, b...)
	}
	next.Sources = append(next.Sources, st)
	sort.Slice(next.Yields, func(i, j int) bool { return next.Yields[i].ID < next.Yields[j].ID })
	sort.Slice(next.Borrows, func(i, j int) bool { return next.Borrows[i].ID < next.Borrows[j].ID })
	sort.Slice(next.Sources, func(i, j int) bool { return next.Sources[i].Name < next.Sources[j].Name })
	c.current = next
}
func (c *MarketCache) requestRefresh() {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, ch := range c.refresh {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
func (c *MarketCache) start(ctx context.Context, jobs []sourceJob, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	for _, job := range jobs {
		trigger := make(chan struct{}, 1)
		c.mu.Lock()
		c.refresh = append(c.refresh, trigger)
		c.mu.Unlock()
		go func() {
			// Each source has its own schedule and publishes as soon as it finishes.
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			var lastStart time.Time
			fetch := func() {
				if time.Since(lastStart) < 10*time.Second {
					return
				}
				lastStart = time.Now()
				child, cancel := context.WithTimeout(ctx, 45*time.Second)
				y, b, err := job.run(child)
				cancel()
				if ctx.Err() == nil {
					c.publish(job.name, y, b, err)
				}
				// Requests received during this fetch are already satisfied by it.
				for {
					select {
					case <-trigger:
					default:
						return
					}
				}
			}
			fetch()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					fetch()
				case <-trigger:
					fetch()
				}
			}
		}()
	}
}
