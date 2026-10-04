package main

import (
	"math"
	"strings"
	"testing"
	"time"
)

func alertFixture() (Config, Snapshot, time.Time) {
	now := time.Now().UTC()
	c := Config{StaleAfter: 10 * time.Minute, Cooldown: 15 * time.Minute}
	s := Snapshot{Sources: []SourceStatus{{Name: "Exponent yields", OK: true}, {Name: "Kamino", OK: true}, {Name: "Loopscale", OK: true}}, Yields: []YieldMarket{{ID: "y", Product: "yt-onyc", Name: "YT ONyc", APY: ptr(10), FetchedAt: now, Maturity: now.Add(24 * time.Hour)}}, Borrows: []BorrowMarket{{ID: "b", Platform: "Kamino", Name: "ONyc", AvailableUSDC: ptr(1000), BorrowAPY: ptr(8), FetchedAt: now}}}
	return c, s, now
}
func TestThresholdAndAvailabilityDoNotSpam(t *testing.T) {
	c, s, now := alertFixture()
	u := subscribedTestUser(1)
	u.APYThreshold = ptr(9)
	a := evaluateAlerts(u, s, now, c)
	if len(a) != 2 {
		t.Fatal(a)
	}
	for _, x := range a {
		u.Alerts[x.Key] = x.NewState
	}
	if len(evaluateAlerts(u, s, now.Add(time.Minute), c)) != 0 {
		t.Fatal("duplicate alerts")
	}
	// A failure to deliver must not set the active latch.
	fresh := subscribedTestUser(2)
	fresh.APYThreshold = ptr(9)
	evaluateAlerts(fresh, s, now, c)
	if len(evaluateAlerts(fresh, s, now, c)) != 2 {
		t.Fatal("unsent notification was acknowledged")
	}
}
func TestRearmAndCooldown(t *testing.T) {
	c, s, now := alertFixture()
	u := subscribedTestUser(1)
	u.APYThreshold = ptr(9)
	for _, a := range evaluateAlerts(u, s, now, c) {
		u.Alerts[a.Key] = a.NewState
	}
	s.Yields[0].APY = ptr(8)
	s.Borrows[0].AvailableUSDC = ptr(0)
	evaluateAlerts(u, s, now.Add(time.Minute), c)
	s.Yields[0].APY = ptr(10)
	s.Borrows[0].AvailableUSDC = ptr(1000)
	if len(evaluateAlerts(u, s, now.Add(2*time.Minute), c)) != 0 {
		t.Fatal("cooldown ignored")
	}
	later := now.Add(16 * time.Minute)
	s.Yields[0].FetchedAt = later
	s.Borrows[0].FetchedAt = later
	if len(evaluateAlerts(u, s, later, c)) != 2 {
		t.Fatal("did not rearm")
	}
}
func TestStaleFailedPausedMaturedAndRateUnknown(t *testing.T) {
	c, s, now := alertFixture()
	u := subscribedTestUser(1)
	u.APYThreshold = ptr(1)
	s.Yields[0].Maturity = now.Add(-time.Second)
	s.Borrows[0].FetchedAt = now.Add(-time.Hour)
	if len(evaluateAlerts(u, s, now, c)) != 0 {
		t.Fatal("stale/mature data alerted")
	}
	c, s, now = alertFixture()
	u.Paused = true
	if len(evaluateAlerts(u, s, now, c)) != 0 {
		t.Fatal("paused user alerted")
	}
	u.Paused = false
	s.Sources = nil
	if len(evaluateAlerts(u, s, now, c)) != 0 {
		t.Fatal("failed sources alerted")
	}
	c, s, now = alertFixture()
	s.Yields = nil
	s.Borrows[0].BorrowAPY = nil
	u.MaxBorrowAPY = ptr(9)
	if len(evaluateAlerts(u, s, now, c)) != 0 {
		t.Fatal("unknown rate accepted")
	}
	u.MaxBorrowAPY = nil
	s.Borrows[0].SourceAt = now.Add(-time.Hour)
	if len(evaluateAlerts(u, s, now, c)) != 0 {
		t.Fatal("stale provider timestamp accepted")
	}
}
func TestChangeBaselineAccumulatesAndFilters(t *testing.T) {
	c, s, now := alertFixture()
	s.Borrows = nil
	u := subscribedTestUser(1)
	if len(evaluateAlerts(u, s, now, c)) != 0 {
		t.Fatal("baseline should be silent")
	}
	s.Yields[0].APY = ptr(10.6)
	if len(evaluateAlerts(u, s, now, c)) != 0 {
		t.Fatal("small change")
	}
	s.Yields[0].APY = ptr(11.1)
	if len(evaluateAlerts(u, s, now, c)) != 1 {
		t.Fatal("cumulative change lost")
	}
	u.Muted["y"] = true
	if len(evaluateAlerts(u, s, now, c)) != 0 {
		t.Fatal("muted market alerted")
	}
}
func TestSettingValidation(t *testing.T) {
	u := subscribedTestUser(1)
	u.Pending = "amount"
	for _, s := range []string{"NaN", "Inf", "-1", "0", "abc", "10000000000"} {
		if applySetting(u, s) == nil {
			t.Fatalf("accepted %q", s)
		}
	}
	if err := applySetting(u, "1000"); err != nil || u.MinUSDC != 1000 || u.Pending != "" {
		t.Fatal("amount setting failed")
	}
	u.Pending = "borrow"
	if e := applySetting(u, "off"); e != nil || u.MaxBorrowAPY != nil {
		t.Fatal("off failed")
	}
	if _, e := validSetting("NaN", 0, math.MaxFloat64); e == nil {
		t.Fatal("accepted NaN")
	}
}
func TestRenderingBoundsAndStaleLabels(t *testing.T) {
	c, s, now := alertFixture()
	u := subscribedTestUser(1)
	s.Yields[0].FetchedAt = now.Add(-time.Hour)
	if !strings.Contains(yieldText(s, u, c, now), "STALE") {
		t.Fatal("stale label missing")
	}
	for _, p := range chunks(strings.Repeat("🌟", 9000)) {
		if len([]rune(p)) > 1800 {
			t.Fatal("oversized Telegram chunk")
		}
	}
}

func subscribedTestUser(id int64) *User {
	u := newUser(id)
	u.APYDirection = "up"
	u.APYAlertsDisabled, u.USDCAlertsDisabled = false, false
	u.APYMarketID, u.APYMarketName = "y", "YT ONyc"
	return u
}
