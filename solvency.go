package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Production endpoint selected by the OnRe dashboard's runtime config.
const solvencyURL = "https://onre.accountable.capital:8443/dashboard"
const solvencyDashboard = "https://onre.accountable.capital/"

type reserveValue struct {
	Value *float64 `json:"value"`
	Tag   string   `json:"tag"`
}
type reserveSource struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Exposure  string `json:"filter"`
	Frequency string `json:"frequency"`
	Updated   string `json:"lastUpdated"`
}
type solvencyData struct {
	Ratio    *float64 `json:"collateralization"`
	TS       int64    `json:"ts"`
	Reserves struct {
		Supply      reserveValue                                        `json:"total_supply"`
		Allocation  map[string]map[string]map[string]map[string]float64 `json:"reserves_breakdown"`
		SupplySplit map[string]map[string]json.RawMessage               `json:"supply_split"`
		Total       reserveValue                                        `json:"total_reserves"`
		Split       map[string]map[string]reserveValue                  `json:"reserves_split"`
	} `json:"reserves"`
	Sources map[string]reserveSource `json:"dataSources"`
}
type solvencyCache struct {
	mu      sync.RWMutex
	data    *solvencyData // Immutable after publication.
	fetched time.Time
	failed  bool
}

func validReserve(v *float64) bool {
	return v != nil && !math.IsNaN(*v) && !math.IsInf(*v, 0) && *v >= 0
}
func parseSolvency(raw []byte) (*solvencyData, error) {
	var envelope struct {
		Res  string        `json:"res"`
		Data *solvencyData `json:"data"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Res != "ok" || envelope.Data == nil {
		return nil, fmt.Errorf("invalid Accountable response")
	}
	d := envelope.Data
	if !validReserve(d.Reserves.Total.Value) || !validReserve(d.Reserves.Supply.Value) || d.TS <= 0 {
		return nil, fmt.Errorf("missing Accountable totals or timestamp")
	}
	if d.Ratio != nil && !validReserve(d.Ratio) {
		return nil, fmt.Errorf("invalid collateral ratio")
	}
	// Do not accidentally connect a different project's dashboard.
	found := false
	for _, assets := range d.Reserves.SupplySplit {
		if _, ok := assets["ONyc"]; ok {
			found = true
		}
	}
	if !found {
		return nil, fmt.Errorf("OnRe supply identity missing")
	}
	for _, assets := range d.Reserves.Split {
		for _, a := range assets {
			if !validReserve(a.Value) {
				return nil, fmt.Errorf("invalid reserve breakdown")
			}
		}
	}
	for _, groups := range d.Reserves.Allocation {
		for _, venues := range groups {
			for _, assets := range venues {
				for _, v := range assets {
					if !validReserve(&v) {
						return nil, fmt.Errorf("invalid allocation")
					}
				}
			}
		}
	}
	return d, nil
}
func fetchSolvency(ctx context.Context, client *http.Client, url string) (*solvencyData, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; ONycWatch/1.9)")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Accountable connection failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Accountable HTTP %d", resp.StatusCode)
	}
	const limit = 4 << 20
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || len(raw) > limit {
		return nil, fmt.Errorf("invalid Accountable response size")
	}
	return parseSolvency(raw)
}
func (c *solvencyCache) publish(d *solvencyData, err error, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failed = err != nil || d == nil
	if !c.failed {
		c.data = d
		c.fetched = now
	}
}
func (b *Bot) refreshSolvency(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		child, cancel := context.WithTimeout(ctx, 20*time.Second)
		d, err := fetchSolvency(child, b.API.Client, solvencyURL)
		cancel()
		if ctx.Err() != nil {
			return
		}
		b.solvency.publish(d, err, time.Now().UTC())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func solvencyKeyboard(section string) *Keyboard {
	if section != "" {
		return &Keyboard{Rows: [][]Button{{{Text: "← Back", Data: "solvency"}}}}
	}
	return &Keyboard{Rows: [][]Button{
		{{Text: "💰 Overview", Data: "solvency:overview"}},
		{{Text: "📊 Reserve Breakdown", Data: "solvency:reserves"}},
		{{Text: "🏦 Capital Allocation", Data: "solvency:allocation"}},
		{{Text: "🔍 Verification & Sources", Data: "solvency:sources"}},
		{{Text: "🌐 View OnRe Dashboard", URL: solvencyDashboard}},
		{{Text: "← Main Menu", Data: "home"}},
	}}
}
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
func reserveUSD(v float64) string { return "$" + formatAmount(v) }
func reserveShare(v, total float64) string {
	if total <= 0 {
		return "share unavailable"
	}
	return fmt.Sprintf("%.2f%% of total reserves", v/total*100)
}
func reserveTimestamp(ms int64) string {
	return time.UnixMilli(ms).UTC().Format("2006-01-02 15:04:05 UTC")
}
func sourceFreshness(s reserveSource, now time.Time) string {
	ms, err := strconv.ParseInt(s.Updated, 10, 64)
	if err != nil || ms <= 0 {
		return "Unavailable"
	}
	age := now.Sub(time.UnixMilli(ms))
	if age < -5*time.Minute {
		return "Timestamp ahead of clock"
	}
	var interval time.Duration
	switch strings.ToUpper(strings.TrimSpace(s.Frequency)) {
	case "15 MIN":
		interval = 15 * time.Minute
	case "WEEKLY":
		interval = 7 * 24 * time.Hour
	case "DAILY":
		interval = 24 * time.Hour
	}
	if interval == 0 {
		return "Schedule not evaluated"
	}
	if age > interval+interval/2 {
		return "Delayed"
	}
	return "Within expected interval"
}
func (b *Bot) solvencyText(section string, now time.Time) string {
	if section == "" {
		return "OnRe · Independent Proof of Solvency\nData provided by Accountable.\n\nExplore the reserves backing ONyc, capital allocation and source updates.\nThe bot displays reported data; it does not independently verify cryptographic proofs."
	}
	titles := map[string]string{"overview": "Overview", "reserves": "Reserve Breakdown", "allocation": "Capital Allocation", "sources": "Verification & Sources"}
	var out strings.Builder
	fmt.Fprintf(&out, "OnRe · %s\n", titles[section])
	b.solvency.mu.RLock()
	d, fetched, failed := b.solvency.data, b.solvency.fetched, b.solvency.failed
	b.solvency.mu.RUnlock()
	if d == nil {
		return out.String() + "\nAccountable data is unavailable or still loading. Please try again shortly."
	}
	age := now.Sub(time.UnixMilli(d.TS))
	if failed || now.Sub(fetched) > 15*time.Minute || age > 30*time.Minute || age < -5*time.Minute {
		out.WriteString("[STALE / UNAVAILABLE] Last available snapshot.\n")
	}
	total := *d.Reserves.Total.Value
	switch section {
	case "overview":
		fmt.Fprintf(&out, "\nTotal protocol reserves: %s\nTotal supply (USD): %s\n", reserveUSD(total), reserveUSD(*d.Reserves.Supply.Value))
		if d.Ratio != nil {
			fmt.Fprintf(&out, "Reported collateral ratio: %.2f%%\n", *d.Ratio*100)
		} else {
			out.WriteString("Reported collateral ratio: Unavailable\n")
		}
		fmt.Fprintf(&out, "Reserves minus supply: %s\n", reserveUSD(total-*d.Reserves.Supply.Value))
		out.WriteString("\nSupply is the dashboard's USD valuation of ONyc, not token count. These figures are separate from the bot's AUM source.")
	case "reserves":
		if len(d.Reserves.Split) == 0 {
			out.WriteString("\nReserve breakdown unavailable.")
		}
		for _, venue := range sortedKeys(d.Reserves.Split) {
			fmt.Fprintf(&out, "\n%s\n", venue)
			for _, asset := range sortedKeys(d.Reserves.Split[venue]) {
				a := d.Reserves.Split[venue][asset]
				fmt.Fprintf(&out, "• %s: %s (%s)\n", asset, reserveUSD(*a.Value), reserveShare(*a.Value, total))
			}
		}
	case "allocation":
		groups := d.Reserves.Allocation["Total Reserves"]
		if len(groups) == 0 {
			out.WriteString("\nCapital allocation unavailable.")
		}
		for _, group := range sortedKeys(groups) {
			sum := 0.0
			for _, assets := range groups[group] {
				for _, v := range assets {
					sum += v
				}
			}
			fmt.Fprintf(&out, "\n%s: %s (%s)\n", group, reserveUSD(sum), reserveShare(sum, total))
			for _, venue := range sortedKeys(groups[group]) {
				v := 0.0
				for _, x := range groups[group][venue] {
					v += x
				}
				fmt.Fprintf(&out, "• %s: %s (%s)\n", venue, reserveUSD(v), reserveShare(v, total))
			}
		}
	case "sources":
		if len(d.Sources) == 0 {
			out.WriteString("\nSource details unavailable.")
		}
		for _, key := range sortedKeys(d.Sources) {
			s := d.Sources[key]
			name := s.Name
			if name == "" {
				name = key
			}
			ts := "Unavailable"
			if ms, e := strconv.ParseInt(s.Updated, 10, 64); e == nil && ms > 0 {
				ts = reserveTimestamp(ms)
			}
			fmt.Fprintf(&out, "\n%s\nType: %s · %s\nSchedule: %s\nSource updated: %s\nStatus: %s\n", name, s.Type, s.Exposure, s.Frequency, ts, sourceFreshness(s, now))
		}
		out.WriteString("\nConnection types are reported by Accountable, including unsigned reports. Status describes timing only; the bot does not validate proofs.")
	}
	fmt.Fprintf(&out, "\n\nDashboard retrieval: %s\n%s\nSource: Accountable · OnRe\nIndividual sources may update less frequently than the dashboard.", reserveTimestamp(d.TS), updated(fetched))
	return out.String()
}
func (b *Bot) handleSolvency(ctx context.Context, u *User, action, text string) (bool, error) {
	if action == "" {
		words := strings.Fields(text)
		if len(words) > 0 && strings.Split(words[0], "@")[0] == "/solvency" {
			action = "solvency"
		}
	}
	section := ""
	switch action {
	case "solvency":
	case "solvency:overview", "solvency:reserves", "solvency:allocation", "solvency:sources":
		section = strings.TrimPrefix(action, "solvency:")
	default:
		return false, nil
	}
	return true, b.TG.send(ctx, u.ChatID, b.solvencyText(section, time.Now()), solvencyKeyboard(section))
}
