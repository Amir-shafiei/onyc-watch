package main

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

type expYield struct {
	VaultAddress, TokenName, MarketStatus, YtMint, PtMint         string
	UnderlyingAsset                                               struct{ Mint, Ticker string }
	ImpliedApy, UnderlyingApy, YtPriceInAsset, YtHolderRewardsApy Number
	MaturityDateUnixTs                                            int64
}
type expTranche struct {
	Address, MintBase, MintLpSenior, MintLpJunior string
	MarketState, StatusFlags                      int
	SeniorApy, JuniorApy, UnderlyingApy           Number
}

func (a *API) exponentYields(ctx context.Context) ([]YieldMarket, error) {
	var raw []expYield
	if err := a.get(ctx, a.Exponent+"/api/markets", nil, &raw); err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, fmt.Errorf("missing Exponent markets array")
	}
	now := time.Now().UTC()
	out := []YieldMarket{}
	for _, r := range raw {
		if r.TokenName != "ONyc" && r.TokenName != "srONyc" {
			continue
		}
		if r.MarketStatus != "active" || r.MaturityDateUnixTs <= now.Unix() {
			continue
		}
		if r.VaultAddress == "" || r.YtMint == "" || !r.ImpliedApy.Valid || !r.UnderlyingApy.Valid {
			return nil, fmt.Errorf("missing required Exponent yield fields")
		}
		product := "yt-onyc"
		if r.TokenName == "srONyc" {
			product = "yt-sronyc"
		}
		out = append(out, YieldMarket{ID: safeID("exp", r.VaultAddress), Product: product, Name: "YT " + r.TokenName, Mint: r.YtMint, URL: a.Exponent + "/en", Metric: "Market implied APY (not YT return)", APY: pct(r.ImpliedApy), UnderlyingAPY: pct(r.UnderlyingApy), RewardsAPY: pct(r.YtHolderRewardsApy), Maturity: time.Unix(r.MaturityDateUnixTs, 0).UTC(), FetchedAt: now})
		if r.YtPriceInAsset.Valid {
			out[len(out)-1].YTPrice = ptr(r.YtPriceInAsset.Value)
		}
	}
	return out, nil
}
func (a *API) exponentTranches(ctx context.Context) ([]YieldMarket, error) {
	var raw []expTranche
	if err := a.get(ctx, a.Exponent+"/api/tranching-markets", nil, &raw); err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, fmt.Errorf("missing Exponent tranches array")
	}
	now := time.Now().UTC()
	out := []YieldMarket{}
	for _, r := range raw {
		if r.MintBase != ONYC {
			continue
		}
		if r.Address == "" || !r.SeniorApy.Valid || !r.JuniorApy.Valid {
			return nil, fmt.Errorf("missing required tranche fields")
		}
		for _, x := range []struct {
			key, name, mint string
			value           Number
		}{{"sronyc", "srONyc", r.MintLpSenior, r.SeniorApy}, {"jronyc", "jrONyc", r.MintLpJunior, r.JuniorApy}} {
			out = append(out, YieldMarket{ID: safeID("exp", r.Address, x.key), Product: x.key, Name: x.name, Mint: x.mint, URL: a.Exponent + "/en", Metric: "Current tranche APY (variable estimate)", APY: pct(x.value), UnderlyingAPY: pct(r.UnderlyingApy), FetchedAt: now})
		}
	}
	return out, nil
}

type capCounter struct{ Capacity, CurrentTotal Number }
type kaminoStats struct {
	Token, Status, RateType  string
	ActualAvailableLiquidity Number
	BorrowApy                struct{ Current Number }
	FixedTerm                *struct {
		TermSeconds int64
		Apy         Number
	}
	BorrowCaps struct {
		DebtOutsideElevationGroup capCounter
		DebtAgainstCollateral     []struct {
			capCounter
			CollateralReserve string
			ElevationGroup    int
		}
	}
}
type kaminoPair struct {
	LendingMarket, Name, CollateralReserve, CollateralMint string
	Slugs                                                  []string
	BorrowReserveTerms                                     []struct {
		Reserve        string
		ElevationGroup int
		MaxLtv         Number
	}
}

func applyCap(v float64, c capCounter) float64 {
	if !c.Capacity.Valid {
		return v
	}
	if !c.CurrentTotal.Valid {
		return 0
	}
	return math.Min(v, math.Max(0, c.Capacity.Value-c.CurrentTotal.Value))
}
func kaminoCapacity(s kaminoStats, collateral string, group int) *float64 {
	if !s.ActualAvailableLiquidity.Valid {
		return nil
	}
	v := math.Max(0, s.ActualAvailableLiquidity.Value)
	if group == 0 {
		v = applyCap(v, s.BorrowCaps.DebtOutsideElevationGroup)
	} else {
		found := false
		for _, c := range s.BorrowCaps.DebtAgainstCollateral {
			if c.CollateralReserve == collateral && c.ElevationGroup == group {
				v = applyCap(v, c.capCounter)
				found = true
			}
		}
		if !found {
			return nil
		} // Never infer a pair-specific capacity from a pool total.
	}
	return &v
}
func (a *API) kamino(ctx context.Context) ([]BorrowMarket, error) {
	var raw struct{ CollateralReserves []kaminoPair }
	if err := a.get(ctx, a.Kamino+"/markets/collateral-reserves", nil, &raw); err != nil {
		return nil, err
	}
	if raw.CollateralReserves == nil {
		return nil, fmt.Errorf("missing collateral reserve list")
	}
	out := []BorrowMarket{}
	cache := map[string]kaminoStats{}
	for _, p := range raw.CollateralReserves {
		if !onycName(p.Name) {
			continue
		}
		for _, term := range p.BorrowReserveTerms {
			if !term.MaxLtv.Valid || term.MaxLtv.Value <= 0 {
				continue
			}
			s, ok := cache[term.Reserve]
			if !ok {
				if err := a.get(ctx, a.Kamino+"/reserves/"+term.Reserve+"/stats", nil, &s); err != nil {
					return nil, err
				}
				cache[term.Reserve] = s
			}
			if s.Token != USDC || s.Status != "Active" {
				continue
			}
			now := time.Now().UTC()
			termLabel := "Variable rate"
			if s.FixedTerm != nil {
				termLabel = fmt.Sprintf("Fixed term: %dh", s.FixedTerm.TermSeconds/3600)
			}
			out = append(out, BorrowMarket{ID: safeID("kamino", p.CollateralReserve, term.Reserve, fmt.Sprint(term.ElevationGroup)), Platform: "Kamino", Name: strings.TrimSuffix(p.Name, " Borrow Market") + " / USDC", CollateralMint: p.CollateralMint, URL: "https://app.kamino.finance/", Basis: "Cap-aware reserve liquidity", Term: termLabel, AvailableUSDC: kaminoCapacity(s, p.CollateralReserve, term.ElevationGroup), BorrowAPY: pct(s.BorrowApy.Current), FetchedAt: now, Note: "Shared liquidity. Wallet borrowing power and complete loop execution are not checked."})
		}
	}
	return out, nil
}

type loopInfo struct {
	CollateralMint, PrincipalMint, Name string
	PrincipalAmountAvailable, WAvgApy   Number
	StatsLastUpdated                    int64
	FeVisible                           bool
}

// Loop info is a reported capacity feed, NOT an executable borrow quote.
// Do not derive a borrow APY from leveraged APYs or subtract unrelated yields.
func (a *API) loopscale(ctx context.Context) ([]BorrowMarket, error) {
	var raw map[string]loopInfo
	if err := a.get(ctx, a.Loopscale+"/markets/loop/info", map[string]any{}, &raw); err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, fmt.Errorf("missing Loopscale loop map")
	}
	out := []BorrowMarket{}
	now := time.Now().UTC()
	for slug, r := range raw {
		if !onycName(r.Name) || r.PrincipalMint != USDC || !r.FeVisible {
			continue
		}
		// Known PT loop slugs carry a maturity such as onyc-10jan27-usdc.
		expired := false
		for _, part := range strings.Split(slug, "-") {
			if t, err := time.Parse("02Jan06", strings.ToUpper(part[:min(len(part), 2)])+titleMonth(part)); err == nil && t.Before(now) {
				expired = true
			}
		}
		if expired {
			continue
		}
		if r.CollateralMint == "" || !r.PrincipalAmountAvailable.Valid || r.PrincipalAmountAvailable.Value < 0 || r.StatsLastUpdated <= 0 {
			return nil, fmt.Errorf("missing required Loopscale fields")
		}
		b := BorrowMarket{ID: "loopscale:" + slug, Platform: "Loopscale", Name: r.Name, CollateralMint: r.CollateralMint, URL: "https://app.loopscale.com/loops/" + slug, Basis: "Reported loop liquidity (not an executable quote)", AvailableUSDC: ptr(r.PrincipalAmountAvailable.Value / 1e6), SourceAt: time.Unix(r.StatsLastUpdated, 0).UTC(), FetchedAt: now, Note: "Capacity may be shared across loops. Confirm current borrowing terms on Loopscale."}
		if r.WAvgApy.Valid {
			b.LoopAPY = ptr(r.WAvgApy.Value)
		} // This field is already a percentage.
		out = append(out, b)
	}
	a.enrichLoopQuotes(ctx, out)
	return out, nil
}
func titleMonth(s string) string {
	if len(s) != 7 {
		return ""
	}
	return strings.ToUpper(s[2:3]) + strings.ToLower(s[3:5]) + s[5:]
}

type sourceJob struct {
	name string
	run  func(context.Context) ([]YieldMarket, []BorrowMarket, error)
}

func (a *API) jobs() []sourceJob {
	return []sourceJob{
		{"Exponent yields", func(c context.Context) ([]YieldMarket, []BorrowMarket, error) {
			y, e := a.exponentYields(c)
			return y, nil, e
		}},
		{"Exponent tranches", func(c context.Context) ([]YieldMarket, []BorrowMarket, error) {
			y, e := a.exponentTranches(c)
			return y, nil, e
		}},
		{"Kamino", func(c context.Context) ([]YieldMarket, []BorrowMarket, error) { b, e := a.kamino(c); return nil, b, e }},
		{"Loopscale", func(c context.Context) ([]YieldMarket, []BorrowMarket, error) {
			b, e := a.loopscale(c)
			return nil, b, e
		}},
	}
}

func (a *API) collect(ctx context.Context) Snapshot {
	s := Snapshot{FetchedAt: time.Now().UTC()}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, job := range a.jobs() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, cancel := context.WithTimeout(ctx, 45*time.Second)
			defer cancel()
			y, b, err := job.run(c)
			mu.Lock()
			defer mu.Unlock()
			st := SourceStatus{Name: job.name, OK: err == nil, FetchedAt: time.Now().UTC()}
			if err != nil {
				st.Error = err.Error()
			} else {
				s.Yields = append(s.Yields, y...)
				s.Borrows = append(s.Borrows, b...)
			}
			s.Sources = append(s.Sources, st)
		}()
	}
	wg.Wait()
	sort.Slice(s.Yields, func(i, j int) bool { return s.Yields[i].ID < s.Yields[j].ID })
	sort.Slice(s.Borrows, func(i, j int) bool { return s.Borrows[i].ID < s.Borrows[j].ID })
	sort.Slice(s.Sources, func(i, j int) bool { return s.Sources[i].Name < s.Sources[j].Name })
	return s
}
