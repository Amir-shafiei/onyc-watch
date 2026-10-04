package main

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

const (
	USDC = "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"
	ONYC = "5Y8NV33Vv7WbnLfq3zBcKSdYPrk7g2KoiQoe7M2tcxp5"
)

// Number accepts the numeric strings returned by Kamino without treating a
// missing or null field as zero. Rates in the internal model are percentages.
type Number struct {
	Value float64
	Valid bool
}

func (n *Number) UnmarshalJSON(b []byte) error {
	n.Valid = false
	if string(b) == "null" {
		return nil
	}
	s := string(b)
	if len(s) > 0 && s[0] == '"' {
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return fmt.Errorf("invalid numeric API field")
	}
	n.Value = v
	n.Valid = true
	return nil
}
func pct(n Number) *float64 {
	if !n.Valid {
		return nil
	}
	v := n.Value * 100
	return &v
}
func ptr(v float64) *float64 { return &v }
func rate(v *float64) string {
	if v == nil {
		return "Unavailable"
	}
	return fmt.Sprintf("%.2f%%", *v)
}

type YieldMarket struct {
	ID, Product, Name, Mint, URL, Metric    string
	APY, UnderlyingAPY, YTPrice, RewardsAPY *float64
	Maturity, FetchedAt                     time.Time
}
type BorrowQuote struct {
	APY, AvailableUSDC, LTV float64
	Term                    string
	FetchedAt               time.Time
}
type BorrowMarket struct {
	Quotes                                               []BorrowQuote
	QuoteStatus                                          string
	ID, Platform, Name, CollateralMint, URL, Basis, Term string
	AvailableUSDC, BorrowAPY, LoopAPY                    *float64
	SourceAt, FetchedAt                                  time.Time
	Note                                                 string
}
type SourceStatus struct {
	Name      string
	OK        bool
	Error     string
	FetchedAt time.Time
}
type Snapshot struct {
	Yields    []YieldMarket
	Borrows   []BorrowMarket
	Sources   []SourceStatus
	FetchedAt time.Time
}

func (s Snapshot) sourceOK(name string) bool {
	for _, x := range s.Sources {
		if x.Name == name {
			return x.OK
		}
	}
	return false
}
func isFresh(t, now time.Time, maxAge time.Duration) bool {
	return !t.IsZero() && now.Sub(t) <= maxAge && t.Sub(now) <= time.Minute
}
func mature(t, now time.Time) bool  { return !t.IsZero() && !t.After(now) }
func onycName(s string) bool        { return strings.Contains(strings.ToLower(s), "onyc") }
func safeID(parts ...string) string { return strings.Join(parts, ":") }
