package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCachePublishesFastSourceWithoutWaitingForSlow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := newMarketCache(Snapshot{})
	slowStarted := make(chan struct{})
	slowExited := make(chan struct{})
	c.start(ctx, []sourceJob{
		{name: "Loopscale", run: func(ctx context.Context) ([]YieldMarket, []BorrowMarket, error) {
			close(slowStarted)
			<-ctx.Done()
			close(slowExited)
			return nil, nil, ctx.Err()
		}},
		{name: "Exponent yields", run: func(ctx context.Context) ([]YieldMarket, []BorrowMarket, error) {
			return []YieldMarket{{ID: "fast", Product: "yt-onyc", APY: ptr(12), FetchedAt: time.Now()}}, nil, nil
		}},
	}, time.Hour)
	<-slowStarted
	deadline := time.After(time.Second)
	for !c.snapshot().sourceOK("Exponent yields") {
		select {
		case <-deadline:
			t.Fatal("fast data was blocked by the slow source")
		case <-time.After(time.Millisecond):
		}
	}
	if len(c.snapshot().Yields) != 1 {
		t.Fatal("fast source did not publish")
	}
	cancel()
	select {
	case <-slowExited:
	case <-time.After(time.Second):
		t.Fatal("worker did not cancel")
	}
}

func TestCacheFailureKeepsTimestampAndDoesNotEraseOtherSources(t *testing.T) {
	_, seed, now := alertFixture()
	c := newMarketCache(seed)
	before := c.snapshot()
	c.publish("Exponent yields", nil, nil, errors.New("timeout"))
	after := c.snapshot()
	if after.sourceOK("Exponent yields") || !after.Yields[0].FetchedAt.Equal(now) || len(after.Borrows) != 1 {
		t.Fatal("failure corrupted cache")
	}
	if !before.sourceOK("Exponent yields") {
		t.Fatal("published snapshot was mutated")
	}
	c.publish("Exponent yields", nil, nil, nil)
	if len(c.snapshot().Yields) != 0 || len(c.snapshot().Borrows) != 1 {
		t.Fatal("successful empty response did not remove only its own rows")
	}
}

func TestCachedCommandsDoNotCallProviders(t *testing.T) {
	var marketRequests atomic.Int32
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		marketRequests.Add(1)
		http.Error(w, "must not be called", 500)
	}))
	defer source.Close()
	var texts []string
	tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Record only response texts; all requests are serial within each command.
		var payload struct {
			Text string `json:"text"`
		}
		json.NewDecoder(r.Body).Decode(&payload)
		if payload.Text != "" {
			texts = append(texts, payload.Text)
		}
		w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer tg.Close()
	cfg, s, _ := alertFixture()
	b := &Bot{Config: cfg, API: &API{Client: source.Client(), Exponent: source.URL, Kamino: source.URL, Loopscale: source.URL}, TG: &Telegram{Client: tg.Client(), Base: tg.URL}, State: &State{Users: map[int64]*User{}, Snapshot: s}, Cache: newMarketCache(s)}
	for _, cmd := range []string{"/apy", "/usdc", "/refresh"} {
		m := &TGMessage{Text: cmd, From: TGUser{ID: 1}}
		m.Chat.ID = 1
		m.Chat.Type = "private"
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := b.handle(ctx, Update{Message: m})
		cancel()
		if err != nil {
			t.Fatalf("%s failed: %v", cmd, err)
		}
	}
	if marketRequests.Load() != 0 {
		t.Fatal("a user command waited for a provider")
	}
	if len(texts) != 3 {
		t.Fatalf("expected one message per command; got %d", len(texts))
	}
	if !strings.Contains(texts[2], "Updating in the background") {
		t.Fatal("refresh must explain cached data")
	}
}

func TestAPYHasNoRepeatedLinksAndFitsOneMessage(t *testing.T) {
	s := Snapshot{Sources: []SourceStatus{{Name: "Exponent yields", OK: true}, {Name: "Exponent tranches", OK: true}}}
	for _, p := range products {
		s.Yields = append(s.Yields, YieldMarket{ID: p.key, Product: p.key, Name: p.label, Metric: "Current APY", APY: ptr(10), UnderlyingAPY: ptr(11), FetchedAt: time.Now(), URL: "https://app.exponent.finance/en"})
	}
	text := yieldText(s, subscribedTestUser(1), Config{StaleAfter: time.Minute}, time.Now())
	if strings.Contains(text, "https://app.exponent.finance") {
		t.Fatal("APY contains repeated links")
	}
	if len(chunks(text)) != 1 {
		t.Fatal("standard APY view should use one Telegram message")
	}
}
