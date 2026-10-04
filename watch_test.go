package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIndependentWatchTargets(t *testing.T) {
	c, s, now := alertFixture()
	u := newUser(1)
	addRule(u, WatchRule{Kind: "apy", MarketID: s.Yields[0].ID, Direction: "up", Target: *s.Yields[0].APY})
	addRule(u, WatchRule{Kind: "apy", MarketID: s.Yields[0].ID, Direction: "down", Target: *s.Yields[0].APY - 1})
	got := evaluateAlerts(u, s, now, c)
	if len(got) != 1 || got[0].Key != "watch:1:apy" {
		t.Fatalf("wrong equal-target alerts: %+v", got)
	}
	b := &Bot{State: &State{Users: map[int64]*User{1: u}}}
	receipt := alertDelivery{user: u, preferences: preferences(u, got[0].Key), alert: got[0]}
	// Editing a different rule cannot cause a delivered one-shot alert to repeat.
	u.Rules[1].Target = 99
	u.Rules[1].Revision++
	b.finishAlert(receipt)
	if len(u.Rules) != 1 || u.Rules[0].ID != "2" {
		t.Fatal("wrong rule removed")
	}
	u.Rules[0].Disabled = true
	if len(evaluateAlerts(u, s, now, c)) != 0 {
		t.Fatal("paused rule sent")
	}
	u.Rules[0].Disabled = false
	u.Paused = true
	if len(evaluateAlerts(u, s, now, c)) != 0 {
		t.Fatal("global pause ignored")
	}
	u.Paused = false
	s.Yields[0].FetchedAt = now.Add(-24 * time.Hour)
	if len(evaluateAlerts(u, s, now, c)) != 0 {
		t.Fatal("stale APY sent")
	}
}
func TestWatchEditAndFailureDuringDelivery(t *testing.T) {
	c, s, now := alertFixture()
	u := newUser(1)
	addRule(u, WatchRule{Kind: "apy", MarketID: s.Yields[0].ID, Direction: "up", Target: 0})
	a := evaluateAlerts(u, s, now, c)[0]
	b := &Bot{State: &State{Users: map[int64]*User{1: u}}}
	r := alertDelivery{user: u, preferences: preferences(u, a.Key), alert: a, err: &TelegramError{Code: 500}}
	b.finishAlert(r)
	if len(u.Rules) != 1 {
		t.Fatal("failed delivery removed alert")
	}
	r.err = nil
	u.Rules[0].Revision++
	u.Rules[0].Target = 999
	b.finishAlert(r)
	if len(u.Rules) != 1 || u.Rules[0].Target != 999 {
		t.Fatal("in-flight receipt removed edited alert")
	}
}
func TestMaturityStagesAndPersistence(t *testing.T) {
	c, s, now := alertFixture()
	u := newUser(1)
	addRule(u, WatchRule{Kind: "maturity", Maturity: now.Add(6 * 24 * time.Hour), Name: "YT ONyc"})
	a := evaluateAlerts(u, s, now, c)
	if len(a) != 1 || !strings.HasSuffix(a[0].Key, ":7") {
		t.Fatal(a)
	}
	finishWatch(u, a[0])
	if len(evaluateAlerts(u, s, now, c)) != 0 {
		t.Fatal("7-day repeated")
	}
	a = evaluateAlerts(u, s, now.Add(5*24*time.Hour), c)
	if len(a) != 1 || !strings.HasSuffix(a[0].Key, ":1") {
		t.Fatal(a)
	}
	finishWatch(u, a[0])
	dir := t.TempDir()
	state := &State{Version: 1, Users: map[int64]*User{1: u}}
	if err := state.save(dir); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(evaluateAlerts(loaded.Users[1], s, now.Add(5*24*time.Hour), c)) != 0 {
		t.Fatal("reminder duplicated after restart")
	}
	if len(evaluateAlerts(u, s, now.Add(7*24*time.Hour), c)) != 0 {
		t.Fatal("expired reminder sent")
	}
}
func TestNewMarketsBaselineOutageAndReceipts(t *testing.T) {
	c, s, now := alertFixture()
	for i := range s.Sources {
		s.Sources[i].FetchedAt = now
	}
	u := newUser(1)
	u.NewMarkets = true
	if len(evaluateAlerts(u, s, now, c)) != 0 {
		t.Fatal("existing markets announced")
	}
	y := s.Yields[0]
	y.ID = "new-market"
	s.Yields = append(s.Yields, y)
	a := evaluateAlerts(u, s, now, c)
	if len(a) != 1 || a[0].Key != "new:new-market" {
		t.Fatal(a)
	}
	// Failed sends must retry; successful receipts persist deduplication.
	if len(evaluateAlerts(u, s, now, c)) != 1 {
		t.Fatal("lost retry")
	}
	finishWatch(u, a[0])
	if len(evaluateAlerts(u, s, now, c)) != 0 {
		t.Fatal("duplicate market")
	}
	outage := s
	outage.Yields = nil
	for i := range outage.Sources {
		outage.Sources[i].OK = false
	}
	evaluateAlerts(u, outage, now, c)
	for i := range s.Sources {
		s.Sources[i].OK = true
	}
	if len(evaluateAlerts(u, s, now, c)) != 0 {
		t.Fatal("outage rearmed discovery")
	}
}
func TestWatchUIExplicitDirectionAndCancel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"ok":true,"result":true}`)) }))
	defer server.Close()
	c, s, _ := alertFixture()
	u := newUser(1)
	b := &Bot{Config: c, State: &State{Users: map[int64]*User{1: u}, Snapshot: s}, TG: &Telegram{Client: server.Client(), Base: server.URL}}
	action := func(a string) {
		t.Helper()
		var up Update
		json.Unmarshal([]byte(`{"callback_query":{"id":"c","data":"`+a+`","from":{"id":1},"message":{"chat":{"id":1,"type":"private"}}}}`), &up)
		if e := b.handle(context.Background(), up); e != nil {
			t.Fatal(e)
		}
	}
	input := func(v string) {
		m := &TGMessage{Text: v, From: TGUser{ID: 1}}
		m.Chat.ID = 1
		m.Chat.Type = "private"
		if e := b.handle(context.Background(), Update{Message: m}); e != nil {
			t.Fatal(e)
		}
	}
	action("wadd")
	action("wpick:" + keyHash(s.Yields[0].ID))
	action("wdir:down")
	input("999")
	if len(u.Rules) != 1 || u.Rules[0].Direction != "down" || u.Rules[0].Target != 999 {
		t.Fatal("direction inferred instead of explicit choice")
	}
	action("wedit:1")
	action("wdir:up")
	input("NaN")
	if u.Rules[0].Target != 999 {
		t.Fatal("invalid target saved")
	}
	input("/cancel")
	if u.Rules[0].Direction != "down" {
		t.Fatal("cancel changed rule")
	}
	action("wtoggle:1")
	if !u.Rules[0].Disabled {
		t.Fatal("toggle failed")
	}
	action("wdelete:1")
	if len(u.Rules) != 0 {
		t.Fatal("delete failed")
	}
	b.callbacks.Wait()
}
func TestAUMValidationAndFailure(t *testing.T) {
	for _, raw := range []string{"null", "NaN", "0", "-1", "<html>", "{}", "1e999"} {
		if _, err := parseAUM([]byte(raw)); err == nil {
			t.Fatal("accepted", raw)
		}
	}
	for _, raw := range []string{"123456789.12", `"123456789.12"`} {
		v, err := parseAUM([]byte(raw))
		if err != nil || v != 123456789.12 {
			t.Fatal(v, err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("123456789.12")) }))
	defer server.Close()
	if v, err := fetchAUM(context.Background(), server.Client(), server.URL); err != nil || v != 123456789.12 {
		t.Fatal(v, err)
	}
	b := &Bot{Config: Config{StaleAfter: time.Minute}}
	if !strings.Contains(b.aumText(), "unavailable") {
		t.Fatal("invented unavailable AUM")
	}
	b.aum.value = 123456789
	b.aum.fetched = time.Now().Add(-time.Hour)
	if !strings.Contains(b.aumText(), "STALE") {
		t.Fatal("stale AUM shown as current")
	}
}
