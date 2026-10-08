package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func solvencyFixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/solvency.json")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func TestSolvencyLiveContract(t *testing.T) {
	d, err := parseSolvency(solvencyFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if *d.Reserves.Total.Value != 296144817.42 || *d.Reserves.Supply.Value != 293240663.29 || *d.Ratio != 1.009904 {
		t.Fatal("incorrect totals or ratio units")
	}
	now := time.UnixMilli(d.TS)
	b := &Bot{}
	b.solvency.publish(d, nil, now)
	overview := b.solvencyText("overview", now)
	if !strings.Contains(overview, "100.99%") || !strings.Contains(overview, "$2,904,154.13") {
		t.Fatal(overview)
	}
	breakdown := b.solvencyText("reserves", now)
	// Percentages must use total reserves, not the parent venue's total.
	if !strings.Contains(breakdown, "USDC (kamino): $10,099,689.57 (3.41% of total reserves)") {
		t.Fatal(breakdown)
	}
	allocation := b.solvencyText("allocation", now)
	if !strings.Contains(allocation, "On-Chain: $139,226,266.74") || !strings.Contains(allocation, "Off-Chain: $156,918,550.68") {
		t.Fatal(allocation)
	}
	sources := b.solvencyText("sources", now)
	if !strings.Contains(sources, "Report (Unsigned)") || !strings.Contains(sources, "2026-10-06 00:00:00 UTC") {
		t.Fatal(sources)
	}
	if strings.Contains(sources, "Status: Delayed") {
		t.Fatal("weekly source incorrectly marked delayed", sources)
	}
}
func TestSolvencyRejectsInvalidData(t *testing.T) {
	for _, raw := range []string{`{}`, `{"res":"ok","data":null}`, `<html>error</html>`} {
		if _, err := parseSolvency([]byte(raw)); err == nil {
			t.Fatal("accepted", raw)
		}
	}
	for _, mutate := range []func(map[string]any){
		func(d map[string]any) { delete(d["reserves"].(map[string]any), "total_supply") },
		func(d map[string]any) { d["ts"] = 0 },
		func(d map[string]any) { d["reserves"].(map[string]any)["supply_split"] = map[string]any{} },
		func(d map[string]any) { d["collateralization"] = -1 },
		func(d map[string]any) { d["reserves"].(map[string]any)["total_reserves"] = map[string]any{"value": -2} },
	} {
		var e map[string]any
		_ = json.Unmarshal(solvencyFixture(t), &e)
		mutate(e["data"].(map[string]any))
		raw, _ := json.Marshal(e)
		if _, err := parseSolvency(raw); err == nil {
			t.Fatal("accepted malformed contract")
		}
	}
}
func TestSolvencyCacheOutagesAndAge(t *testing.T) {
	b := &Bot{}
	if !strings.Contains(b.solvencyText("overview", time.Now()), "unavailable or still loading") {
		t.Fatal("missing loading state")
	}
	d, err := parseSolvency(solvencyFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	now := time.UnixMilli(d.TS)
	b.solvency.publish(d, nil, now)
	b.solvency.publish(nil, errors.New("offline"), now.Add(time.Minute))
	text := b.solvencyText("overview", now.Add(time.Minute))
	if !strings.Contains(text, "STALE / UNAVAILABLE") || !strings.Contains(text, "296,144,817.42") {
		t.Fatal(text)
	}
	b.solvency.publish(d, nil, now.Add(time.Hour))
	if !strings.Contains(b.solvencyText("overview", now.Add(time.Hour)), "STALE / UNAVAILABLE") {
		t.Fatal("old source refreshed as current")
	}
	for _, key := range []string{"Ethereum Wallets", "ONyc Contract"} {
		if sourceFreshness(d.Sources[key], now.Add(time.Hour)) != "Delayed" {
			t.Fatal(key)
		}
	}
	if sourceFreshness(d.Sources["Clarien Bank"], now.Add(time.Hour)) != "Within expected interval" {
		t.Fatal("weekly freshness")
	}
}
func TestSolvencyHTTPAndMenu(t *testing.T) {
	raw := solvencyFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fail" {
			w.WriteHeader(503)
			return
		}
		w.Write(raw)
	}))
	defer server.Close()
	if _, err := fetchSolvency(context.Background(), server.Client(), server.URL); err != nil {
		t.Fatal(err)
	}
	if _, err := fetchSolvency(context.Background(), server.Client(), server.URL+"/fail"); err == nil {
		t.Fatal("accepted failed HTTP")
	}
	calls := 0
	tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.Write([]byte(`{"ok":true,"result":{}}`)) }))
	defer tg.Close()
	b := &Bot{TG: &Telegram{Client: tg.Client(), Base: tg.URL}}
	for _, action := range []string{"solvency", "solvency:overview", "solvency:reserves", "solvency:allocation", "solvency:sources"} {
		ok, err := b.handleSolvency(context.Background(), newUser(1), action, "")
		if !ok || err != nil {
			t.Fatal(action, err)
		}
	}
	ok, err := b.handleSolvency(context.Background(), newUser(1), "", "/solvency@mybot")
	if !ok || err != nil || calls != 6 {
		t.Fatal("command routing", err, calls)
	}
	urls := 0
	for _, row := range solvencyKeyboard("").Rows {
		for _, v := range row {
			if v.URL != "" {
				urls++
				if v.URL != solvencyDashboard {
					t.Fatal(v)
				}
			}
		}
	}
	if urls != 1 {
		t.Fatal("dashboard link missing or duplicated")
	}
	for _, row := range solvencyKeyboard("overview").Rows {
		for _, v := range row {
			if v.URL != "" || v.Data != "solvency" {
				t.Fatal("subpage navigation", v)
			}
		}
	}
}
