package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type API struct {
	Client                      *http.Client
	Exponent, Kamino, Loopscale string
}

func newAPI() *API {
	return &API{Client: &http.Client{Timeout: 20 * time.Second}, Exponent: "https://app.exponent.finance", Kamino: "https://api.kamino.finance", Loopscale: "https://tars.loopscale.com/v1"}
}
func (a *API) get(ctx context.Context, url string, body any, out any) error {
	var r io.Reader
	method := "GET"
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
		method = "POST"
	}
	req, err := http.NewRequestWithContext(ctx, method, url, r)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "ONycWatch/1.0 (+read-only market monitor)")
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.Client.Do(req)
	if err != nil {
		return fmt.Errorf("market API connection failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("market API HTTP %d", resp.StatusCode)
	}
	dec := json.NewDecoder(io.LimitReader(resp.Body, 8<<20))
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("invalid market API response: %w", err)
	}
	return nil
}
