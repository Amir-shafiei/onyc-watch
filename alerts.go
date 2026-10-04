package main

import (
	"fmt"
	"math"
	"time"
)

type Alert struct {
	Key, Text string
	NewState  AlertState
}

func yieldUsable(s Snapshot, y YieldMarket, now time.Time, age time.Duration) bool {
	return s.sourceOK(yieldSource(y)) && isFresh(y.FetchedAt, now, age) && !mature(y.Maturity, now) && y.APY != nil
}
func borrowUsable(s Snapshot, b BorrowMarket, now time.Time, age time.Duration) bool {
	if !s.sourceOK(b.Platform) || !isFresh(b.FetchedAt, now, age) || b.AvailableUSDC == nil {
		return false
	}
	return b.SourceAt.IsZero() || isFresh(b.SourceAt, now, age)
}
func evaluateAlerts(u *User, s Snapshot, now time.Time, c Config) []Alert {
	if u.Paused {
		return nil
	}
	out := evaluateWatch(u, s, now, c)
	for _, y := range s.Yields {
		if u.APYMarketID != y.ID || u.APYAlertsDisabled || !u.Products[y.Product] || u.Muted[y.ID] || !yieldUsable(s, y, now, c.StaleAfter) {
			continue
		}
		k := "apy:" + y.ID
		st := u.Alerts[k]
		value := *y.APY
		hit := false
		reason := ""
		if u.APYThreshold != nil {
			// Legacy targets acquire a direction from their first fresh observed rate.
			if u.APYDirection == "" {
				u.APYDirection = "up"
				if *u.APYThreshold < value {
					u.APYDirection = "down"
				}
			}
			active := value >= *u.APYThreshold
			relation := "at or above"
			if u.APYDirection == "down" {
				active = value <= *u.APYThreshold
				relation = "at or below"
			}
			if !active {
				st.Active = false
				u.Alerts[k] = st
				continue
			}
			hit = !st.Active
			reason = fmt.Sprintf("Target reached (%s): %.2f%%", relation, *u.APYThreshold)
		} else {
			if st.LastValue == nil {
				st.LastValue = ptr(value)
				u.Alerts[k] = st
				continue
			}
			hit = u.APYChange > 0 && math.Abs(value-*st.LastValue) >= u.APYChange
			reason = fmt.Sprintf("Changed by %+.2f percentage points since the last alert/baseline", value-*st.LastValue)
		}
		if hit && now.Sub(st.LastSent) >= c.Cooldown {
			st.Active = true
			st.LastSent = now
			st.LastValue = ptr(value)
			out = append(out, Alert{Key: k, Text: fmt.Sprintf("📊 APY alert\n%s\n%s: %s\n%s\n%s\n%s", yieldTitle(y), y.Metric, rate(y.APY), reason+"\nOne-time alert completed and removed.", updated(y.FetchedAt), y.URL), NewState: st})
		}
	}
	for _, b := range s.Borrows {
		if u.USDCAlertsDisabled || !u.Platforms[b.Platform] || u.Muted[b.ID] || !borrowUsable(s, b, now, c.StaleAfter) {
			continue
		}
		quoteText := ""
		if b.Platform == "Loopscale" {
			b.BorrowAPY = nil
			if q := matchingLoopQuote(b, u.MinUSDC, u.MaxBorrowAPY, now, c.StaleAfter); q != nil {
				b.BorrowAPY = ptr(q.APY)
				quoteText = "\n" + quoteDescription(q)
			}
		}
		// Unknown rate cannot satisfy a user-specified maximum borrow rate.
		if u.MaxBorrowAPY != nil && b.BorrowAPY == nil {
			continue
		}
		active := *b.AvailableUSDC >= u.MinUSDC && *b.AvailableUSDC > 0
		if u.MaxBorrowAPY != nil {
			active = active && *b.BorrowAPY <= *u.MaxBorrowAPY
		}
		k := "usdc:" + b.ID
		st := u.Alerts[k]
		if !active {
			st.Active = false
			u.Alerts[k] = st
			continue
		}
		if !st.Active && now.Sub(st.LastSent) >= c.Cooldown {
			st.Active = true
			st.LastSent = now
			text := fmt.Sprintf("💵 USDC availability alert\n%s · %s\nReported capacity: %.2f USDC\nBorrow APY: %s\nBasis: %s\n%s\nNot reserved; check the market before borrowing.\n%s", b.Platform, b.Name, *b.AvailableUSDC, rate(b.BorrowAPY), b.Basis+quoteText, updated(b.FetchedAt), b.URL)
			out = append(out, Alert{Key: k, Text: text, NewState: st})
		}
	}
	return out
}
