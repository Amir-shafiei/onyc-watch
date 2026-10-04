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

func TestAPYDoesNotWaitForCallbackOrAlert(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Text string `json:"text"`
		}
		json.NewDecoder(r.Body).Decode(&payload)
		if r.URL.Path == "/answerCallbackQuery" || strings.Contains(payload.Text, "availability alert") {
			started <- struct{}{}
			<-release
		}
		w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer server.Close()
	defer close(release)
	c, s, _ := alertFixture()
	b := &Bot{Config: c, State: &State{Users: map[int64]*User{1: subscribedTestUser(1)}, Snapshot: s}, TG: &Telegram{Client: server.Client(), Base: server.URL}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b.notify(ctx)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("alert did not start")
	}
	var up Update
	json.Unmarshal([]byte(`{"callback_query":{"id":"cb","data":"apy","from":{"id":1},"message":{"chat":{"id":1,"type":"private"}}}}`), &up)
	done := make(chan error, 1)
	go func() { done <- b.handle(ctx, up) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("APY waited for blocked alert or callback")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("callback not acknowledged")
	}
}

func TestAlertCompletionRespectsCurrentSettingsAndCondition(t *testing.T) {
	for _, mode := range []string{"success", "pause", "delete", "threshold", "condition", "failure"} {
		t.Run(mode, func(t *testing.T) {
			c, s, _ := alertFixture()
			u := subscribedTestUser(1)
			b := &Bot{Config: c, State: &State{Users: map[int64]*User{1: u}, Snapshot: s}, sending: true}
			a := evaluateAlerts(u, s, time.Now(), c)[0]
			r := alertDelivery{user: u, preferences: preferences(u, a.Key), alert: a}
			switch mode {
			case "pause":
				u.Paused = true
			case "delete":
				delete(b.State.Users, 1)
			case "threshold":
				u.MinUSDC = 2000
			case "condition":
				b.State.Snapshot.Borrows[0].AvailableUSDC = ptr(0)
			case "failure":
				r.err = &TelegramError{Code: 429, RetryAfter: 10}
			}
			b.finishAlert(r)
			if b.sending {
				t.Fatal("delivery slot not released")
			}
			if u.Alerts[a.Key].Active != (mode == "success") {
				t.Fatal("incorrect delivery state")
			}
			if mode == "success" && len(evaluateAlerts(u, s, time.Now(), c)) != 0 {
				t.Fatal("duplicate delivery")
			}
			if mode == "failure" && time.Until(b.nextAlert) < 9*time.Second {
				t.Fatal("retry-after ignored")
			}
		})
	}
}
