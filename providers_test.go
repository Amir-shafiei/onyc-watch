package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, e := os.ReadFile("testdata/" + name)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func testAPI(t *testing.T, routes map[string][]byte) *API {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(b)
	}))
	t.Cleanup(server.Close)
	return &API{Client: server.Client(), Exponent: server.URL, Kamino: server.URL, Loopscale: server.URL}
}
func TestRealExponentResponses(t *testing.T) {
	var raw []map[string]any
	if e := json.Unmarshal(fixture(t, "exponent-yields.json"), &raw); e != nil {
		t.Fatal(e)
	}
	for _, r := range raw {
		r["maturityDateUnixTs"] = time.Now().Add(24 * time.Hour).Unix()
	}
	body, _ := json.Marshal(raw)
	a := testAPI(t, map[string][]byte{"/api/markets": body, "/api/tranching-markets": fixture(t, "exponent-tranches.json")})
	y, e := a.exponentYields(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if len(y) != 2 {
		t.Fatalf("want both YTs, got %d", len(y))
	}
	for _, m := range y {
		if m.APY == nil || *m.APY < 1 || !strings.Contains(m.Metric, "not YT return") {
			t.Fatalf("wrong percentage/label: %+v", m)
		}
	}
	tr, e := a.exponentTranches(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if len(tr) != 2 {
		t.Fatal("missing tranche")
	}
	if *tr[0].APY < 7 || *tr[0].APY > 8 || *tr[1].APY < 18 {
		t.Fatal("wrong tranche rate conversion")
	}
}
func TestExponentMissingIsNotZero(t *testing.T) {
	body := fmt.Sprintf(`[{"vaultAddress":"abc","tokenName":"ONyc","ytMint":"yt","marketStatus":"active","maturityDateUnixTs":%d,"underlyingApy":0.1}]`, time.Now().Add(time.Hour).Unix())
	a := testAPI(t, map[string][]byte{"/api/markets": []byte(body)})
	if _, e := a.exponentYields(context.Background()); e == nil {
		t.Fatal("missing implied APY must be an error")
	}
}
func TestMaturedExponentExcluded(t *testing.T) {
	a := testAPI(t, map[string][]byte{"/api/markets": []byte(`[{"tokenName":"ONyc","marketStatus":"active","maturityDateUnixTs":1}]`)})
	y, e := a.exponentYields(context.Background())
	if e != nil || len(y) != 0 {
		t.Fatal(y, e)
	}
}
func TestRealLoopscaleUnitsAndExpiration(t *testing.T) {
	a := testAPI(t, map[string][]byte{"/markets/loop/info": fixture(t, "loopscale.json")})
	rows, e := a.loopscale(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, r := range rows {
		if r.ID == "loopscale:onyc-usdc" {
			found = true
			if *r.AvailableUSDC < 1e6 || *r.AvailableUSDC > 2e6 {
				t.Fatal("incorrect USDC decimals")
			}
			if *r.LoopAPY < 16 || *r.LoopAPY > 17 {
				t.Fatal("loop APY was multiplied twice")
			}
			if r.BorrowAPY != nil {
				t.Fatal("must not invent borrow rate")
			}
		}
		if strings.Contains(r.ID, "13may26") && time.Now().Year() >= 2027 {
			t.Fatal("expired loop retained")
		}
	}
	if !found {
		t.Fatal("missing ONyc USDC")
	}
}
func TestKaminoCapsAndMissingValues(t *testing.T) {
	var s kaminoStats
	if e := json.Unmarshal(fixture(t, "kamino-stats.json"), &s); e != nil {
		t.Fatal(e)
	}
	if s.Token != USDC {
		t.Fatal("wrong mint")
	}
	v := kaminoCapacity(s, "collateral", 0)
	if v == nil || *v < 0 {
		t.Fatal("bad available capacity")
	}
	if kaminoCapacity(s, "unknown", 4) != nil {
		t.Fatal("unknown elevation group must not claim liquidity")
	}
	s.ActualAvailableLiquidity = Number{Value: 500, Valid: true}
	s.BorrowCaps.DebtOutsideElevationGroup = capCounter{Capacity: Number{100, true}, CurrentTotal: Number{80, true}}
	if *kaminoCapacity(s, "c", 0) != 20 {
		t.Fatal("pair cap not applied")
	}
	s.ActualAvailableLiquidity = Number{}
	if kaminoCapacity(s, "c", 0) != nil {
		t.Fatal("missing amount represented as zero")
	}
}
func TestKaminoProviderUsesVerifiedPairs(t *testing.T) {
	pairs := []byte(`{"collateralReserves":[{"name":"srONyc Borrow Market","collateralMint":"sr","collateralReserve":"coll","borrowReserveTerms":[{"reserve":"debt","maxLtv":"0.7","elevationGroup":0}]}]}`)
	stats := []byte(`{"token":"` + USDC + `","status":"Active","rateType":"variable","actualAvailableLiquidity":"1234.5","borrowApy":{"current":"0.08"},"borrowCaps":{"debtOutsideElevationGroup":{"capacity":null,"currentTotal":"10"}}}`)
	a := testAPI(t, map[string][]byte{"/markets/collateral-reserves": pairs, "/reserves/debt/stats": stats})
	rows, e := a.kamino(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if len(rows) != 1 || *rows[0].BorrowAPY != 8 || *rows[0].AvailableUSDC != 1234.5 {
		t.Fatal(rows)
	}
}
func TestSourceFailureAndMerge(t *testing.T) {
	a := testAPI(t, map[string][]byte{})
	s := a.collect(context.Background())
	for _, st := range s.Sources {
		if st.OK {
			t.Fatal("404 accepted")
		}
	}
	old := Snapshot{Yields: []YieldMarket{{ID: "x", Product: "sronyc", FetchedAt: time.Unix(100, 0)}}}
	merged := mergeSnapshot(old, s)
	if len(merged.Yields) != 1 || merged.Yields[0].FetchedAt.Unix() != 100 {
		t.Fatal("stale timestamp was rewritten")
	}
	good := Snapshot{Sources: []SourceStatus{{Name: "Exponent tranches", OK: true}}}
	if len(mergeSnapshot(old, good).Yields) != 0 {
		t.Fatal("successful empty response kept obsolete market")
	}
}
