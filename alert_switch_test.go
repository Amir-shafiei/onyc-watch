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

func TestAlertSwitchesIndependentAndPersistent(t *testing.T) {
	c, s, now := alertFixture()
	u := subscribedTestUser(1)
	u.APYThreshold = ptr(9)
	for _, tc := range []struct {
		apy, usdc bool
		count     int
		prefix    string
	}{
		{false, false, 2, ""}, {true, false, 1, "usdc:"}, {false, true, 1, "apy:"}, {true, true, 0, ""},
	} {
		u.APYAlertsDisabled, u.USDCAlertsDisabled = tc.apy, tc.usdc
		got := evaluateAlerts(u, s, now, c)
		if len(got) != tc.count {
			t.Fatalf("wrong enabled groups: %+v", tc)
		}
		if tc.count == 1 && !strings.HasPrefix(got[0].Key, tc.prefix) {
			t.Fatal("wrong alert category")
		}
	}
	state := &State{Version: 1, Users: map[int64]*User{1: u}, Snapshot: s}
	dir := t.TempDir()
	if err := state.save(dir); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	v := loaded.Users[1]
	if !v.APYAlertsDisabled || !v.USDCAlertsDisabled || *v.APYThreshold != 9 || v.MinUSDC != 100 {
		t.Fatal("switches or settings lost on restart")
	}
	v.APYAlertsDisabled = false
	v.USDCAlertsDisabled = false
	if len(evaluateAlerts(v, s, now, c)) != 2 {
		t.Fatal("re-enable did not restore alerts")
	}
	var legacy User
	if err := json.Unmarshal([]byte(`{"ChatID":1}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.APYAlertsDisabled || legacy.USDCAlertsDisabled {
		t.Fatal("legacy subscriptions disabled")
	}
}

func TestAlertSwitchCallbacksAndManualAPY(t *testing.T) {
	var texts []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			Text string `json:"text"`
		}
		json.NewDecoder(r.Body).Decode(&p)
		// Callback acknowledgements contain no text; command responses are serial.
		if p.Text != "" {
			texts = append(texts, p.Text)
		}
		w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer server.Close()
	c, s, _ := alertFixture()
	u := subscribedTestUser(1)
	u.APYThreshold = ptr(9)
	b := &Bot{Config: c, State: &State{Users: map[int64]*User{1: u}, Snapshot: s}, TG: &Telegram{Client: server.Client(), Base: server.URL}}
	for _, action := range []string{"alerts:apy:disable", "alerts:apy:disable", "alerts:usdc:disable", "apy"} {
		raw := `{"callback_query":{"id":"cb","data":"` + action + `","from":{"id":1},"message":{"chat":{"id":1,"type":"private"}}}}`
		var up Update
		json.Unmarshal([]byte(raw), &up)
		if err := b.handle(context.Background(), up); err != nil {
			t.Fatal(err)
		}
	}
	b.callbacks.Wait()
	if !u.APYAlertsDisabled || !u.USDCAlertsDisabled || *u.APYThreshold != 9 {
		t.Fatal("disable was not idempotent or settings changed")
	}
	if !strings.Contains(texts[2], "APY alerts: Disabled") || !strings.Contains(texts[2], "USDC alerts: Disabled") {
		t.Fatal("status not shown")
	}
	if !strings.Contains(texts[3], "Current APY") {
		t.Fatal("manual APY disabled")
	}
	if btn := apyAlertKeyboard(u).Rows[0][1]; btn.Text != "Enable" || btn.Data != "alerts:apy:enable" {
		t.Fatal("missing re-enable button")
	}

	// Global pause still overrides enabled categories.
	u.Paused = true
	u.APYAlertsDisabled = false
	u.USDCAlertsDisabled = false
	if len(evaluateAlerts(u, s, time.Now(), c)) != 0 {
		t.Fatal("switch bypassed pause")
	}
}
