package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestLoopQuoteUnitsAndTerms(t *testing.T) {
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Principal                             string
			Collateral                            []string
			Duration, DurationType, Limit, Offset int
		}
		json.NewDecoder(r.Body).Decode(&req)
		if r.URL.Path != "/markets/quote" || req.Principal != USDC || len(req.Collateral) != 1 || req.Collateral[0] != ONYC || req.Limit != 100 || req.Offset != 0 {
			t.Error("wrong quote request")
		}
		if !((req.Duration == 1 && req.DurationType >= 0 && req.DurationType <= 2) || (req.Duration == 3 && req.DurationType == 2)) {
			t.Error("wrong term")
		}
		count.Add(1)
		w.Write(fixture(t, "loopscale-quotes.json"))
	}))
	defer server.Close()
	a := &API{Client: server.Client(), Loopscale: server.URL}
	markets := []BorrowMarket{{Platform: "Loopscale", CollateralMint: ONYC}}
	a.enrichLoopQuotes(context.Background(), markets)
	if count.Load() != 4 || len(markets[0].Quotes) != 12 {
		t.Fatal("missing terms")
	}
	q := markets[0].Quotes[0]
	if q.APY != 7.95 || q.LTV != 70 || q.AvailableUSDC != 1920967.885775 {
		t.Fatalf("incorrect units: %+v", q)
	}
}
func TestLoopQuoteFailurePreservesCapacity(t *testing.T) {
	api := testAPI(t, map[string][]byte{"/markets/quote": []byte(`[{"apy":79500}]`)})
	markets := []BorrowMarket{{Platform: "Loopscale", CollateralMint: ONYC, AvailableUSDC: ptr(123)}}
	api.enrichLoopQuotes(context.Background(), markets)
	if markets[0].BorrowAPY != nil || *markets[0].AvailableUSDC != 123 || !strings.Contains(markets[0].QuoteStatus, "unavailable") {
		t.Fatal("missing fields accepted or capacity lost")
	}
	api = testAPI(t, map[string][]byte{"/markets/quote": []byte(`[]`)})
	quotes, err := api.loopQuotes(context.Background(), ONYC, 1, 0, "1 day")
	if err != nil || len(quotes) != 0 {
		t.Fatal("empty orderbook is valid")
	}
}
func TestLoopRateFilterRequiresEnoughLiquidityAtThatRate(t *testing.T) {
	c, s, now := alertFixture()
	s.Yields = nil
	u := subscribedTestUser(1)
	u.MaxBorrowAPY = ptr(9)
	b := s.Borrows[0]
	b.Platform = "Loopscale"
	b.BorrowAPY = ptr(1)
	b.Quotes = []BorrowQuote{{APY: 1, AvailableUSDC: 10, Term: "1 day", FetchedAt: now}, {APY: 8, AvailableUSDC: 200, Term: "1 week", FetchedAt: now}}
	s.Borrows = []BorrowMarket{b}
	alerts := evaluateAlerts(u, s, now, c)
	if len(alerts) != 1 || !strings.Contains(alerts[0].Text, "8.00%") || !strings.Contains(alerts[0].Text, "1 week") {
		t.Fatal("adequate quote not selected")
	}
	u.MaxBorrowAPY = ptr(7)
	if len(evaluateAlerts(u, s, now, c)) != 0 {
		t.Fatal("tiny cheap quote incorrectly covered requested amount")
	}
	u.MaxBorrowAPY = ptr(9)
	s.Borrows[0].Quotes[1].FetchedAt = now.Add(-time.Hour)
	if len(evaluateAlerts(u, s, now, c)) != 0 {
		t.Fatal("stale quote accepted")
	}
	u.MaxBorrowAPY = nil
	if len(evaluateAlerts(u, s, now, c)) != 1 {
		t.Fatal("capacity-only alert lost")
	}
	text := borrowText(s, u, c, now)
	if !strings.Contains(text, "Unavailable for your minimum USDC") {
		t.Fatal("stale or too-small quote rendered as usable")
	}
}
