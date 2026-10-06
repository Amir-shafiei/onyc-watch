package main

import (
	"strings"
	"testing"
)

func TestDisplayNumbersKeepStableCallbacks(t *testing.T) {
	u := newUser(1)
	for _, name := range []string{"First", "Second", "Third"} {
		addRule(u, WatchRule{Kind: "apy", Name: name})
	}
	_, old := watchList(u)
	oldSecond := old.Rows[2][0].Data
	deleteRule(u, "1")
	text, k := watchList(u)
	if !strings.Contains(text, "#1 Second") || !strings.Contains(text, "#2 Third") || k.Rows[1][0].Text != "Manage #1" || k.Rows[1][0].Data != oldSecond {
		t.Fatal("display numbering changed identity", text, k)
	}
	// The completed rule's old callback must never target the new first entry.
	if ruleByID(u, "1") != nil || ruleByID(u, "2").Name != "Second" {
		t.Fatal("stale callback identity reused")
	}
	finishWatch(u, Alert{Key: "watch:2:apy"})
	text, _ = watchList(u)
	if !strings.Contains(text, "#1 Third") {
		t.Fatal(text)
	}
	deleteRule(u, "3")
	addRule(u, WatchRule{Kind: "apy", Name: "New"})
	text, k = watchList(u)
	if !strings.Contains(text, "#1 New") || k.Rows[1][0].Data != "wview:4" {
		t.Fatal("empty-list numbering or stable ID failed")
	}
}

func TestMarketMenuAndLegacyDisabledSelections(t *testing.T) {
	u := newUser(1)
	s := Snapshot{Yields: []YieldMarket{{ID: "a", Product: "yt-onyc", Name: "YT ONyc"}, {ID: "b", Product: "yt-onyc", Name: "YT ONyc next"}}, Borrows: []BorrowMarket{{ID: "k1", Platform: "Kamino", Name: "ONyc / USDC"}, {ID: "k2", Platform: "Kamino", Name: "Other"}, {ID: "l", Platform: "Loopscale", Name: "ONyc"}}}
	u.Products["yt-onyc"] = false
	u.Platforms["Kamino"] = false
	k := marketKeyboard(u, s)
	if len(k.Rows) != 5 || !strings.HasPrefix(k.Rows[0][0].Text, mark(false)) {
		t.Fatal("duplicate controls or wrong effective selection")
	}
	for _, row := range k.Rows {
		for _, b := range row {
			if strings.HasPrefix(b.Data, "product:") || strings.HasPrefix(b.Data, "platform:") {
				t.Fatal("legacy group control still visible")
			}
		}
	}
	toggleYieldMarket(u, s, s.Yields[0])
	if !u.Products["yt-onyc"] || u.Muted["a"] || !u.Muted["b"] {
		t.Fatal("enabling one market changed sibling selection")
	}
	toggleYieldMarket(u, s, s.Yields[0])
	if !u.Muted["a"] {
		t.Fatal("disable failed")
	}
	toggleBorrowMarket(u, s, s.Borrows[0])
	if !u.Platforms["Kamino"] || u.Muted["k1"] || !u.Muted["k2"] || u.Muted["l"] {
		t.Fatal("platform selection changed unrelated markets")
	}
	sub := borrowMarketKeyboard(u, s, "Kamino")
	if len(sub.Rows) != 3 || sub.Rows[2][0].Data != "markets" {
		t.Fatal("platform submenu has wrong markets")
	}
}
