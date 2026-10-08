package main

import (
	"fmt"
	"strings"
	"time"
)

func updated(t time.Time) string { return "Fetched: " + t.UTC().Format("2006-01-02 15:04:05 UTC") }
func yieldTitle(y YieldMarket) string {
	if y.Maturity.IsZero() {
		return y.Name
	}
	return y.Name + " · " + y.Maturity.UTC().Format("02 Jan 2006")
}
func yieldText(s Snapshot, u *User, c Config, now time.Time) string {
	var b strings.Builder
	b.WriteString("📊 Current APY\nAuto-updated in the background.\n")
	found := map[string]bool{}
	for _, y := range s.Yields {
		if !u.Products[y.Product] || u.Muted[y.ID] {
			continue
		}
		found[y.Product] = true
		flag := ""
		if !yieldUsable(s, y, now, c.StaleAfter) {
			flag = " [STALE / UNAVAILABLE]"
		}
		fmt.Fprintf(&b, "\n%s%s\n%s: %s\n", yieldTitle(y), flag, y.Metric, rate(y.APY))
		if y.UnderlyingAPY != nil {
			fmt.Fprintf(&b, "Underlying APY: %s\n", rate(y.UnderlyingAPY))
		}
		if y.YTPrice != nil {
			fmt.Fprintf(&b, "YT price: %.6f quote-asset units\n", *y.YTPrice)
		}
		if y.RewardsAPY != nil && *y.RewardsAPY != 0 {
			fmt.Fprintf(&b, "YT holder rewards APY (separate): %s\n", rate(y.RewardsAPY))
		}
		fmt.Fprintf(&b, "%s\n", updated(y.FetchedAt))
	}
	for _, p := range products {
		if u.Products[p.key] && !found[p.key] {
			fmt.Fprintf(&b, "\n%s: no current market data (or all maturities muted).\n", p.label)
		}
	}
	b.WriteString("\nYT implied APY is a market pricing rate, NOT the APY earned by buying YT. Tranche APYs are variable estimates. Points are excluded. Source computation timestamps may not be provided.")
	return b.String()
}
func borrowText(s Snapshot, u *User, c Config, now time.Time) string {
	var b strings.Builder
	b.WriteString("💵 USDC Availability\nEach line is a separate collateral / debt market. Do not add shared liquidity across rows.\n")
	n := 0
	for _, m := range s.Borrows {
		if !u.Platforms[m.Platform] || u.Muted[m.ID] {
			continue
		}
		n++
		flag := ""
		if !borrowUsable(s, m, now, c.StaleAfter) {
			flag = " [STALE / UNAVAILABLE]"
		}
		fmt.Fprintf(&b, "\n%s · %s%s\n", m.Platform, m.Name, flag)
		if m.AvailableUSDC == nil {
			b.WriteString("Capacity: Unavailable\n")
		} else {
			fmt.Fprintf(&b, "Reported capacity: %s USDC\n", formatAmount(*m.AvailableUSDC))
		}
		if m.Platform == "Loopscale" {
			if q := matchingLoopQuote(m, u.MinUSDC, nil, now, c.StaleAfter); q != nil {
				fmt.Fprintln(&b, quoteDescription(q))
			} else {
				b.WriteString("Borrow APY: Unavailable for your minimum USDC amount\n")
			}
			fmt.Fprintln(&b, m.QuoteStatus)
		} else {
			fmt.Fprintf(&b, "Borrow APY: %s\n", rate(m.BorrowAPY))
		}
		if m.LoopAPY != nil {
			fmt.Fprintf(&b, "Existing positions' weighted-average loop APY: %s\n", rate(m.LoopAPY))
		}
		if m.Term != "" && m.Platform != "Loopscale" {
			fmt.Fprintln(&b, m.Term)
		}
		fmt.Fprintf(&b, "Basis: %s\n%s\n", m.Basis, updated(m.FetchedAt))
		if !m.SourceAt.IsZero() {
			fmt.Fprintf(&b, "Provider updated: %s\n", m.SourceAt.Format("2006-01-02 15:04:05 UTC"))
		}
		fmt.Fprintf(&b, "%s\n%s\n", m.Note, m.URL)
	}
	if n == 0 {
		b.WriteString("\nNo matching current USDC markets are available in the fetched data.\n")
	}
	b.WriteString("\nAvailability is not an execution guarantee. Unsupported YT/junior collateral is never assumed borrowable.")
	return b.String()
}
func healthText(s Snapshot) string {
	var b strings.Builder
	b.WriteString("Data sources\n")
	for _, x := range s.Sources {
		status := "OK"
		if !x.OK {
			status = "Unavailable: " + x.Error
		}
		fmt.Fprintf(&b, "\n%s: %s\n%s\n", x.Name, status, updated(x.FetchedAt))
	}
	return b.String()
}
func settingsText(u *User) string {
	threshold := "Off (change mode selected)"
	if u.APYThreshold != nil {
		threshold = rate(u.APYThreshold)
	}
	cap := "Off"
	if u.MaxBorrowAPY != nil {
		cap = rate(u.MaxBorrowAPY)
	}
	status := "Active"
	if u.Paused {
		status = "Paused"
	}
	market := u.APYMarketName
	if market == "" {
		market = "Not selected — open APY Alert"
	}
	apyStatus, usdcStatus := "Enabled", "Enabled"
	if u.APYAlertsDisabled {
		apyStatus = "Disabled"
	}
	if u.USDCAlertsDisabled {
		usdcStatus = "Disabled"
	}
	if u.APYMarketID == "" {
		active := 0
		for _, r := range u.Rules {
			if !r.Disabled {
				active++
			}
		}
		return fmt.Sprintf("Alert Settings\nStatus: %s\nMy Alerts: %d saved, %d enabled\nUSDC alerts: %s\nMinimum USDC: %s\nMaximum borrow APY: %s\n\nOpen My Alerts to add or manage independent APY targets and maturity reminders.\nTargets include equality and are removed after successful delivery.\nMaturity reminders are sent at 7 days and 1 day remaining.\nLoopscale rate filters require a fresh quote covering your minimum amount.", status, len(u.Rules), active, usdcStatus, formatAmount(u.MinUSDC), cap)
	}
	return fmt.Sprintf("🔔 Alert Settings\nStatus: %s\nAPY alerts: %s\nAPY market: %s\nUSDC alerts: %s\nAPY threshold: %s\nAPY change: %.2f percentage points\nMinimum USDC: %s\nMaximum borrow APY: %s\n\nAPY threshold uses the displayed metric: implied APY for YT, tranche APY for sr/jr.\nLoopscale rate filters require a fresh quote covering your minimum USDC amount.\nAlerts fire on condition entry, not every poll.", status, apyStatus, market, usdcStatus, threshold, u.APYChange, formatAmount(u.MinUSDC), cap)
}

// Telegram messages are bounded in UTF-16 code units, so use a conservative rune limit.
func chunks(s string) []string {
	const limit = 1800
	var out []string
	var current strings.Builder
	count := 0
	for _, line := range strings.Split(s, "\n") {
		r := []rune(line + "\n")
		for len(r) > 0 {
			room := limit - count
			n := min(room, len(r))
			current.WriteString(string(r[:n]))
			count += n
			r = r[n:]
			if count == limit {
				out = append(out, strings.TrimSpace(current.String()))
				current.Reset()
				count = 0
			}
		}
	}
	if current.Len() > 0 {
		out = append(out, strings.TrimSpace(current.String()))
	}
	return out
}

func apyAlertText(u *User, s Snapshot) string {
	status := "Enabled"
	if u.APYAlertsDisabled {
		status = "Disabled"
	} else if u.Paused {
		status = "Paused — use Resume in the main menu"
	} else {
		if u.Muted[u.APYMarketID] {
			status = "Muted in My Markets"
		}
		for _, m := range s.Yields {
			if m.ID == u.APYMarketID && !u.Products[m.Product] {
				status = "Product disabled in My Markets"
			}
		}
	}
	condition := fmt.Sprintf("Change: %.2f percentage points", u.APYChange)
	if u.APYThreshold != nil {
		relation := "at or above"
		if u.APYDirection == "down" {
			relation = "at or below"
		}
		if u.APYDirection == "" {
			relation = "direction pending fresh rate; target"
		}
		condition = "Target: " + relation + " " + rate(u.APYThreshold)
	}
	return fmt.Sprintf("APY Alert\n\nMarket: %s\n%s\nStatus: %s\n\nOne-time alert: removed after successful delivery. Edit changes the market or target. Disable keeps your settings for later.", u.APYMarketName, condition, status)
}

// formatAmount groups the integer part after rounding to two decimal places.
func formatAmount(v float64) string {
	s := fmt.Sprintf("%.2f", v)
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign = "-"
		s = s[1:]
	}
	whole, fraction, ok := strings.Cut(s, ".")
	if !ok {
		return sign + s
	}
	for i := len(whole) - 3; i > 0; i -= 3 {
		whole = whole[:i] + "," + whole[i:]
	}
	return sign + whole + "." + fraction
}
