package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTelegramCallbackFlowAndPersistence(t *testing.T) {
	var mu sync.Mutex
	var methods []string
	var texts []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		methods = append(methods, r.URL.Path)
		var payload map[string]any
		json.NewDecoder(r.Body).Decode(&payload)
		if v, ok := payload["text"].(string); ok {
			texts = append(texts, v)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer server.Close()
	c, s, _ := alertFixture()
	c.DataDir = t.TempDir()
	state := &State{Version: 1, Users: map[int64]*User{}, Snapshot: s}
	b := &Bot{Config: c, State: state, TG: &Telegram{Client: server.Client(), Base: server.URL}}
	decode := func(raw string) Update {
		var u Update
		if e := json.Unmarshal([]byte(raw), &u); e != nil {
			t.Fatal(e)
		}
		return u
	}
	for _, raw := range []string{
		`{"update_id":1,"message":{"text":"/start","from":{"id":9},"chat":{"id":9,"type":"private"}}}`,
		`{"update_id":2,"callback_query":{"id":"cb","from":{"id":9},"data":"set:amount","message":{"chat":{"id":9,"type":"private"}}}}`,
		`{"update_id":3,"message":{"text":"1200","from":{"id":9},"chat":{"id":9,"type":"private"}}}`,
		`{"update_id":4,"callback_query":{"id":"cb2","from":{"id":9},"data":"apy","message":{"chat":{"id":9,"type":"private"}}}}`,
	} {
		if e := b.handle(context.Background(), decode(raw)); e != nil {
			t.Fatal(e)
		}
	}
	if state.Users[9].MinUSDC != 1200 {
		t.Fatal("conversation setting not applied")
	}
	b.callbacks.Wait()
	if strings.Count(strings.Join(methods, " "), "/answerCallbackQuery") != 2 {
		t.Fatal("callbacks not acknowledged", methods)
	}
	if !strings.Contains(strings.Join(texts, " "), "Current APY") {
		t.Fatal("APY button produced no response")
	}
	if e := state.save(c.DataDir); e != nil {
		t.Fatal(e)
	}
	loaded, e := loadState(c.DataDir)
	if e != nil || loaded.Users[9].MinUSDC != 1200 {
		t.Fatal("state did not survive restart", e)
	}
	loaded.Users[9].MinUSDC = 1300
	if e := loaded.save(c.DataDir); e != nil {
		t.Fatal("atomic replacement failed", e)
	}
}
func TestTelegramTokenRedaction(t *testing.T) {
	tg := &Telegram{Client: &http.Client{Timeout: time.Millisecond}, Base: "http://127.0.0.1:1/botTOPSECRET"}
	e := tg.call(context.Background(), "getMe", map[string]any{}, nil)
	if e == nil || strings.Contains(e.Error(), "TOPSECRET") {
		t.Fatal("token leaked", e)
	}
}
