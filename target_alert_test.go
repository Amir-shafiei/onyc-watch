package main

import (
	"testing"
	"time"
)

func TestDirectionalTargets(t *testing.T) {
	for _, tc := range []struct {
		name                         string
		current, target, before, hit float64
		direction                    string
	}{
		{"fall", 12.91, 12, 12.5, 11.99, "down"},
		{"rise", 12.91, 14, 13.9, 14.1, "up"},
		{"equal", 12, 12, 11.99, 12, "up"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, s, now := alertFixture()
			s.Borrows = nil
			u := subscribedTestUser(1)
			u.Pending = "apy"
			u.PendingMarketID = "y"
			u.PendingMarketName = "YT ONyc"
			text := rate(ptr(tc.target))
			text = text[:len(text)-1]
			if err := applySetting(u, text, ptr(tc.current)); err != nil {
				t.Fatal(err)
			}
			if u.APYDirection != tc.direction {
				t.Fatal("wrong direction")
			}
			s.Yields[0].APY = ptr(tc.before)
			if len(evaluateAlerts(u, s, now, c)) != 0 {
				t.Fatal("fired before target")
			}
			s.Yields[0].APY = ptr(tc.hit)
			if len(evaluateAlerts(u, s, now, c)) != 1 {
				t.Fatal("target crossing missed")
			}
			dir := t.TempDir()
			state := &State{Version: 1, Users: map[int64]*User{1: u}}
			if err := state.save(dir); err != nil {
				t.Fatal(err)
			}
			loaded, err := loadState(dir)
			if err != nil || loaded.Users[1].APYDirection != tc.direction {
				t.Fatal("direction not persisted", err)
			}
		})
	}
}
func TestAPYOneShotDelivery(t *testing.T) {
	for _, mode := range []string{"success", "failure", "replacement", "change"} {
		t.Run(mode, func(t *testing.T) {
			c, s, now := alertFixture()
			s.Borrows = nil
			u := subscribedTestUser(1)
			u.APYThreshold = ptr(9)
			if mode == "change" {
				u.APYThreshold = nil
				u.Alerts["apy:y"] = AlertState{LastValue: ptr(8)}
			}
			a := evaluateAlerts(u, s, now, c)[0]
			receipt := alertDelivery{user: u, preferences: preferences(u, a.Key), alert: a}
			if mode == "failure" {
				receipt.err = &TelegramError{Code: 429, RetryAfter: 1}
			}
			if mode == "replacement" {
				u.APYRevision++
			}
			b := &Bot{Config: c, State: &State{Version: 1, Users: map[int64]*User{1: u}, Snapshot: s}, sending: true}
			// A successful message still consumes the alert when the rate bounces back.
			b.State.Snapshot.Yields[0].APY = ptr(8)
			b.finishAlert(receipt)
			cleared := mode == "success" || mode == "change"
			if (u.APYMarketID == "") != cleared {
				t.Fatal("wrong cleanup result")
			}
			if cleared {
				if !u.APYAlertsDisabled || u.APYThreshold != nil {
					t.Fatal("incomplete cleanup")
				}
				b.State.Snapshot.Yields[0].APY = ptr(20)
				if len(evaluateAlerts(u, b.State.Snapshot, time.Now(), c)) != 0 {
					t.Fatal("one-shot rearmed")
				}
				dir := t.TempDir()
				if err := b.State.save(dir); err != nil {
					t.Fatal(err)
				}
				loaded, err := loadState(dir)
				if err != nil || loaded.Users[1].APYMarketID != "" {
					t.Fatal("deleted alert returned after restart")
				}
			}
		})
	}
}
