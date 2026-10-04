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

// Confirmed against Loopscale's official app bundle: Days=0, Weeks=1, Months=2.
var loopTerms = []struct {
	duration, kind int
	label          string
}{
	{1, 0, "1 day"}, {1, 1, "1 week"}, {1, 2, "1 month"}, {3, 2, "3 months"},
}

type loopRawQuote struct{ APY, LTV, LiquidationThreshold, MaxPrincipalAvailable Number }

func (a *API) loopQuotes(ctx context.Context, mint string, duration, kind int, term string) ([]BorrowQuote, error) {
	var rows []loopRawQuote
	body := map[string]any{"principal": USDC, "collateral": []string{mint}, "duration": duration, "durationType": kind, "limit": 100, "offset": 0}
	if err := a.get(ctx, a.Loopscale+"/markets/quote", body, &rows); err != nil {
		return nil, err
	}
	if rows == nil {
		return nil, fmt.Errorf("missing quote list")
	}
	out := []BorrowQuote{}
	for _, q := range rows {
		if !q.APY.Valid || !q.LTV.Valid || !q.MaxPrincipalAvailable.Valid || !q.LiquidationThreshold.Valid {
			return nil, fmt.Errorf("incomplete quote fields")
		}
		if q.APY.Value < 0 || q.MaxPrincipalAvailable.Value < 0 || q.LTV.Value <= 0 || q.LTV.Value > 1e6 || q.LiquidationThreshold.Value < q.LTV.Value || q.LiquidationThreshold.Value > 1e6 || math.Trunc(q.APY.Value) != q.APY.Value {
			return nil, fmt.Errorf("invalid quote values")
		}
		if q.MaxPrincipalAvailable.Value == 0 {
			continue
		}
		// App's convertOrderbookQuote uses DM(raw)=raw/1e6 as a fraction.
		// Our model uses percent: raw/1e4. USDC quantities use 6 decimals.
		out = append(out, BorrowQuote{APY: q.APY.Value / 1e4, LTV: q.LTV.Value / 1e4, AvailableUSDC: q.MaxPrincipalAvailable.Value / 1e6, Term: term, FetchedAt: time.Now().UTC()})
	}
	return out, nil
}
func (a *API) enrichLoopQuotes(ctx context.Context, markets []BorrowMarket) {
	var wg sync.WaitGroup
	slots := make(chan struct{}, 4)
	var mu sync.Mutex
	failures := make([][]string, len(markets))
	for i := range markets {
		for _, term := range loopTerms {
			wg.Add(1)
			go func(i int, duration, kind int, label string) {
				defer wg.Done()
				select {
				case slots <- struct{}{}:
				case <-ctx.Done():
					mu.Lock()
					failures[i] = append(failures[i], label)
					mu.Unlock()
					return
				}
				defer func() { <-slots }()
				quoteCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
				defer cancel()
				quotes, err := a.loopQuotes(quoteCtx, markets[i].CollateralMint, duration, kind, label)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					failures[i] = append(failures[i], label)
					return
				}
				markets[i].Quotes = append(markets[i].Quotes, quotes...)
			}(i, term.duration, term.kind, term.label)
		}
	}
	wg.Wait()
	for i := range markets {
		m := &markets[i]
		sort.Slice(m.Quotes, func(i, j int) bool {
			a, b := m.Quotes[i], m.Quotes[j]
			if a.APY != b.APY {
				return a.APY < b.APY
			}
			if a.AvailableUSDC != b.AvailableUSDC {
				return a.AvailableUSDC > b.AvailableUSDC
			}
			return a.Term < b.Term
		})
		m.QuoteStatus = "Checked 1 day, 1 week, 1 month and 3 months"
		if len(failures[i]) > 0 {
			sort.Strings(failures[i])
			m.QuoteStatus = "Quotes unavailable for: " + strings.Join(failures[i], ", ")
		}
		if len(m.Quotes) > 0 {
			m.BorrowAPY = ptr(m.Quotes[0].APY)
			m.Term = "Quoted term: " + m.Quotes[0].Term
		}
	}
}
func matchingLoopQuote(m BorrowMarket, minUSDC float64, maxAPY *float64, now time.Time, age time.Duration) *BorrowQuote {
	var best *BorrowQuote
	for _, q := range m.Quotes {
		if !isFresh(q.FetchedAt, now, age) || q.AvailableUSDC < minUSDC || q.AvailableUSDC <= 0 || (maxAPY != nil && q.APY > *maxAPY) {
			continue
		}
		if best == nil || q.APY < best.APY || (q.APY == best.APY && q.AvailableUSDC > best.AvailableUSDC) {
			copy := q
			best = &copy
		}
	}
	return best
}
func quoteDescription(q *BorrowQuote) string {
	return fmt.Sprintf("Quoted borrow APY: %.2f%% · %s\nQuote capacity: %.2f USDC · max LTV: %.2f%%\nQuote fetched: %s", q.APY, q.Term, q.AvailableUSDC, q.LTV, q.FetchedAt.Format("2006-01-02 15:04:05 UTC"))
}
