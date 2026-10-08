package main

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Same live-tvl endpoint used for the AUM header by the official OnRe frontend.
const onreAUMURL = "https://core.api.onre.finance/data/live-tvl"

type aumCache struct {
	mu      sync.RWMutex
	value   float64
	fetched time.Time
	failed  bool
}

func parseAUM(raw []byte) (float64, error) {
	value, err := strconv.ParseFloat(strings.Trim(strings.TrimSpace(string(raw)), "\""), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 {
		return 0, fmt.Errorf("invalid OnRe AUM response")
	}
	return value, nil
}
func fetchAUM(ctx context.Context, client *http.Client, url string) (float64, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json, text/plain")
	req.Header.Set("User-Agent", "ONycWatch/1.8")
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("OnRe connection failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("OnRe HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return 0, err
	}
	return parseAUM(raw)
}
func (b *Bot) refreshAUM(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		child, cancel := context.WithTimeout(ctx, 15*time.Second)
		v, err := fetchAUM(child, b.API.Client, onreAUMURL)
		cancel()
		b.aum.mu.Lock()
		b.aum.failed = err != nil
		if err == nil {
			b.aum.value = v
			b.aum.fetched = time.Now().UTC()
		}
		b.aum.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
func (b *Bot) aumText() string {
	b.aum.mu.RLock()
	defer b.aum.mu.RUnlock()
	if b.aum.fetched.IsZero() {
		return "Current AUM\nOnRe data is unavailable or still loading. Please try again shortly."
	}
	flag := ""
	if b.aum.failed || !isFresh(b.aum.fetched, time.Now(), b.Config.StaleAfter) {
		flag = " [STALE / UNAVAILABLE]"
	}
	return fmt.Sprintf("Current AUM%s\nOnRe: $%.2f million\nUSD: %s\n%s\nSource: OnRe live AUM. Fetch time is not the upstream valuation time.", flag, b.aum.value/1e6, formatAmount(b.aum.value), updated(b.aum.fetched))
}
