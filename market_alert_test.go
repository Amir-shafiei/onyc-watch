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

func TestMarketSelectionOnlyAlertsChosenAPYAndKeepsUSDCHistory(t *testing.T) {
	c, s, now := alertFixture()
	s.Yields = append(s.Yields, YieldMarket{ID: "jr", Product: "jronyc", Name: "jrONyc", APY: ptr(18.3), FetchedAt: now})
	s.Sources = append(s.Sources, SourceStatus{Name: "Exponent tranches", OK: true})
	u := subscribedTestUser(1)
	for _, a := range evaluateAlerts(u, s, now, c) {
		u.Alerts[a.Key] = a.NewState
	}
	usdc := u.Alerts["usdc:b"]
	var texts []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			Text string `json:"text"`
		}
		json.NewDecoder(r.Body).Decode(&p)
		if p.Text != "" {
			texts = append(texts, p.Text)
		}
		w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer server.Close()
	b := &Bot{Config: c, State: &State{Version: 1, Users: map[int64]*User{1: u}, Snapshot: s}, TG: &Telegram{Client: server.Client(), Base: server.URL}}
	callback := func(action string) {
		t.Helper()
		var up Update
		json.Unmarshal([]byte(`{"callback_query":{"id":"cb","data":"`+action+`","from":{"id":1},"message":{"chat":{"id":1,"type":"private"}}}}`), &up)
		if err := b.handle(context.Background(), up); err != nil {
			t.Fatal(err)
		}
	}
	// Existing subscribers open the management panel, then Edit.
	callback("apyalert")
	if !strings.Contains(texts[len(texts)-1], "Status: Enabled") {
		t.Fatal("management panel missing")
	}
	callback("apyalert:edit")
	callback("set:apy")
	if u.Pending != "" || u.APYMarketID != "y" {
		t.Fatal("picker changed active alert")
	}
	callback("pickapy:apy:" + keyHash("jr"))
	if u.PendingMarketID != "jr" || u.APYMarketID != "y" {
		t.Fatal("selection not staged")
	}
	m := &TGMessage{Text: "12.9", From: TGUser{ID: 1}}
	m.Chat.ID, m.Chat.Type = 1, "private"
	if err := b.handle(context.Background(), Update{Message: m}); err != nil {
		t.Fatal(err)
	}
	b.callbacks.Wait()
	if u.APYMarketID != "jr" || u.APYAlertsDisabled || *u.APYThreshold != 12.9 {
		t.Fatal("target not saved")
	}
	if !u.Alerts["usdc:b"].LastSent.Equal(usdc.LastSent) || !u.Alerts["usdc:b"].Active {
		t.Fatal("USDC dedup history lost")
	}
	if len(evaluateAlerts(u, s, now, c)) != 0 {
		t.Fatal("lower target fired before APY fell")
	}
	s.Yields[1].APY = ptr(12.9)
	alerts := evaluateAlerts(u, s, now, c)
	if len(alerts) != 1 || alerts[0].Key != "apy:jr" {
		t.Fatalf("unexpected alerts: %+v", alerts)
	}
	if !strings.Contains(texts[len(texts)-1], "jrONyc") {
		t.Fatal("target missing from confirmation")
	}
	callback("set:change")
	callback("pickapy:change:" + keyHash("y"))
	callback("cancelsetting")
	b.callbacks.Wait()
	if u.APYMarketID != "jr" || *u.APYThreshold != 12.9 {
		t.Fatal("cancel changed alert")
	}
	for _, row := range settingsKeyboard(u).Rows {
		for _, btn := range row {
			if btn.Data == "set:apy" || btn.Data == "set:change" || strings.HasPrefix(btn.Data, "alerts:apy:") {
				t.Fatal("duplicate APY controls in Settings")
			}
		}
	}
	// A new user opens the market picker directly.
	fresh := newUser(2)
	b.State.Users[1] = fresh
	callback("apyalert")
	b.callbacks.Wait()
	if !strings.Contains(texts[len(texts)-1], "Choose the market") {
		t.Fatal("new user did not enter picker")
	}
	b.State.Users[1] = u
	c.DataDir = t.TempDir()
	if err := b.State.save(c.DataDir); err != nil {
		t.Fatal(err)
	}
	restored, err := loadState(c.DataDir)
	if err != nil || restored.Users[1].APYMarketID != "jr" {
		t.Fatal("selected target lost on restart", err)
	}
}

func TestUnrelatedAPYEditDoesNotLoseInflightUSDCReceipt(t *testing.T) {
	c, s, _ := alertFixture()
	u := subscribedTestUser(1)
	a := evaluateAlerts(u, s, time.Now(), c)[0]
	r := alertDelivery{user: u, preferences: preferences(u, a.Key), alert: a}
	u.Pending, u.PendingMarketID, u.PendingMarketName = "apy", "y", "YT ONyc"
	if err := applySetting(u, "9", ptr(10)); err != nil {
		t.Fatal(err)
	}
	b := &Bot{Config: c, State: &State{Users: map[int64]*User{1: u}, Snapshot: s}, sending: true}
	b.finishAlert(r)
	if !u.Alerts[a.Key].Active {
		t.Fatal("APY edit invalidated delivered USDC alert")
	}
}

func TestNewUsersAndLegacyAPYRequireExplicitSelection(t *testing.T) {
	c, s, now := alertFixture()
	u := newUser(1)
	if len(evaluateAlerts(u, s, now, c)) != 0 {
		t.Fatal("new user automatically subscribed")
	}
	u.APYAlertsDisabled = false
	u.APYThreshold = ptr(9)
	if len(evaluateAlerts(u, s, now, c)) != 0 {
		t.Fatal("missing target matched all markets")
	}
	u.Pending = "apy"
	if applySetting(u, "12") == nil {
		t.Fatal("accepted threshold without selected market")
	}
	u.USDCAlertsDisabled = false
	st := &State{Version: 1, Users: map[int64]*User{1: u}}
	dir := t.TempDir()
	if err := st.save(dir); err != nil {
		t.Fatal(err)
	}
	got, err := loadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Users[1].APYAlertsDisabled || got.Users[1].USDCAlertsDisabled || got.Users[1].Pending != "" {
		t.Fatal("legacy migration incorrect")
	}
}
