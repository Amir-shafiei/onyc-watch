package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"time"
)

// Official frontend's live NAV endpoint; not a market execution quote.
const onrePriceURL = "https://core.api.onre.finance/data/live-nav"

func parsePrice(raw []byte) (float64, error) {
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return 0, fmt.Errorf("invalid OnRe NAV response")
	}
	v, err := strconv.ParseFloat(string(number), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
		return 0, fmt.Errorf("invalid OnRe NAV value")
	}
	return v, nil
}

func fetchPrice(ctx context.Context, client *http.Client, url string) (float64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json, text/plain")
	req.Header.Set("User-Agent", "ONycWatch/1.9.2")
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("OnRe NAV connection failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("OnRe NAV HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1025))
	if err != nil || len(raw) > 1024 {
		return 0, fmt.Errorf("invalid OnRe NAV response size")
	}
	return parsePrice(raw)
}

func (b *Bot) publishPrice(v float64, err error, now time.Time) {
	b.price.mu.Lock()
	defer b.price.mu.Unlock()
	b.price.failed = err != nil
	if err == nil {
		b.price.value = v
		b.price.fetched = now
	}
}

func (b *Bot) refreshPrice(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		child, cancel := context.WithTimeout(ctx, 15*time.Second)
		v, err := fetchPrice(child, b.API.Client, onrePriceURL)
		cancel()
		if ctx.Err() != nil {
			return
		}
		b.publishPrice(v, err, time.Now().UTC())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (b *Bot) priceText(now time.Time) string {
	b.price.mu.RLock()
	defer b.price.mu.RUnlock()
	if b.price.fetched.IsZero() {
		return "💵 ONyc Price\nOnRe NAV is unavailable or still loading. Please try again shortly."
	}
	flag := ""
	if b.price.failed || !isFresh(b.price.fetched, now, b.Config.StaleAfter) {
		flag = " [STALE / UNAVAILABLE]"
	}
	return fmt.Sprintf("💵 ONyc Price%s\nNAV per ONyc: $%.6f\n\nSource: OnRe\n%s\nFetch time is not the NAV valuation time.\n\nMarket execution prices may differ.", flag, b.price.value, updated(b.price.fetched))
}
