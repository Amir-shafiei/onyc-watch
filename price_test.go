package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPriceContract(t *testing.T) {
	for _, raw := range []string{"1.153524119", "\"1.153524119\"", " 1.153524119\n"} {
		v, err := parsePrice([]byte(raw))
		if err != nil || v != 1.153524119 {
			t.Fatal(v, err)
		}
	}
	for _, raw := range []string{"null", "0", "-1", "NaN", "\"NaN\"", "1e999", "{}", "[]", "1 2", "<html>error</html>", "\"1.2"} {
		if _, err := parsePrice([]byte(raw)); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
}

func TestPriceOutageAndFormatting(t *testing.T) {
	b := &Bot{Config: Config{StaleAfter: 10 * time.Minute}}
	now := time.Now().UTC()
	if !strings.Contains(b.priceText(now), "still loading") {
		t.Fatal("loading state")
	}
	b.publishPrice(1.153524119, nil, now)
	text := b.priceText(now)
	if !strings.Contains(text, "NAV per ONyc: $1.153524") || strings.Contains(text, "STALE") || !strings.Contains(text, "Market execution prices may differ") {
		t.Fatal(text)
	}
	b.publishPrice(0, errors.New("offline"), now.Add(time.Minute))
	text = b.priceText(now.Add(time.Minute))
	if !strings.Contains(text, "STALE / UNAVAILABLE") || !strings.Contains(text, "$1.153524") || !strings.Contains(text, updated(now)) {
		t.Fatal(text)
	}
	b.publishPrice(1.16, nil, now.Add(2*time.Minute))
	if strings.Contains(b.priceText(now.Add(2*time.Minute)), "STALE") {
		t.Fatal("did not recover")
	}
	if !strings.Contains(b.priceText(now.Add(time.Hour)), "STALE") {
		t.Fatal("old fetch shown as current")
	}
}

func TestPriceHTTP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/error":
			w.WriteHeader(503)
		case "/large":
			w.Write([]byte("1.1" + strings.Repeat(" ", 1024)))
		default:
			w.Write([]byte("1.153524119"))
		}
	}))
	defer server.Close()
	if v, err := fetchPrice(context.Background(), server.Client(), server.URL); err != nil || v != 1.153524119 {
		t.Fatal(v, err)
	}
	for _, path := range []string{"/error", "/large"} {
		if _, err := fetchPrice(context.Background(), server.Client(), server.URL+path); err == nil {
			t.Fatal(path)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fetchPrice(ctx, server.Client(), server.URL); err == nil {
		t.Fatal("ignored cancellation")
	}
}
