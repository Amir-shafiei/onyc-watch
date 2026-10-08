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

type Button struct {
	Text string `json:"text"`
	Data string `json:"callback_data,omitempty"`
	URL  string `json:"url,omitempty"`
}
type Keyboard struct {
	Rows [][]Button `json:"inline_keyboard"`
}
type Telegram struct {
	Client *http.Client
	Base   string
}
type TelegramError struct{ Code, RetryAfter int }

func (e *TelegramError) Error() string { return fmt.Sprintf("Telegram API error %d", e.Code) }
func (t *Telegram) call(ctx context.Context, method string, body any, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", t.Base+"/"+method, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("invalid Telegram configuration")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.Client.Do(req)
	if err != nil {
		return fmt.Errorf("Telegram connection failed")
	}
	defer resp.Body.Close()
	var envelope struct {
		OK         bool            `json:"ok"`
		Code       int             `json:"error_code"`
		Result     json.RawMessage `json:"result"`
		Parameters struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&envelope); err != nil {
		return fmt.Errorf("invalid Telegram response")
	}
	if !envelope.OK {
		code := envelope.Code
		if code == 0 {
			code = resp.StatusCode
		}
		return &TelegramError{Code: code, RetryAfter: envelope.Parameters.RetryAfter}
	}
	if out != nil {
		return json.Unmarshal(envelope.Result, out)
	}
	return nil
}
func (t *Telegram) send(ctx context.Context, id int64, text string, kb *Keyboard) error {
	parts := chunks(text)
	for i, p := range parts {
		payload := map[string]any{"chat_id": id, "text": p, "link_preview_options": map[string]bool{"is_disabled": true}}
		if i == len(parts)-1 && kb != nil {
			payload["reply_markup"] = kb
		}
		if err := t.call(ctx, "sendMessage", payload, nil); err != nil {
			return err
		}
		if i < len(parts)-1 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(1100 * time.Millisecond):
			}
		}
	}
	return nil
}

type TGUser struct {
	ID int64 `json:"id"`
}
type TGMessage struct {
	Text string `json:"text"`
	From TGUser `json:"from"`
	Chat struct {
		ID   int64  `json:"id"`
		Type string `json:"type"`
	} `json:"chat"`
}
type Update struct {
	ID       int64      `json:"update_id"`
	Message  *TGMessage `json:"message"`
	Callback *struct {
		ID      string     `json:"id"`
		From    TGUser     `json:"from"`
		Data    string     `json:"data"`
		Message *TGMessage `json:"message"`
	} `json:"callback_query"`
}

func (t *Telegram) updates(ctx context.Context, offset int64) ([]Update, error) {
	var out []Update
	err := t.call(ctx, "getUpdates", map[string]any{"offset": offset, "timeout": 20, "allowed_updates": []string{"message", "callback_query"}}, &out)
	return out, err
}

func homeKeyboard() *Keyboard {
	return &Keyboard{Rows: [][]Button{
		{{Text: "Current AUM", Data: "aum"}, {Text: "My Alerts", Data: "watch"}},
		{{Text: "Maturity Reminders", Data: "maturities"}, {Text: "New Market Alerts", Data: "newmarkets"}},
		{{Text: "📊 Current APY", Data: "apy"}, {Text: "💵 USDC Availability", Data: "usdc"}},
		{{Text: "🔔 Alert Settings", Data: "settings"}, {Text: "⭐ My Markets", Data: "markets"}},
		{{Text: "🔄 Refresh", Data: "refresh"}, {Text: "⏸ Pause / Resume", Data: "pause"}},
		{{Text: "💵 ONyc Price", Data: "price"}},
		{{Text: "🛡 Proof of Solvency", Data: "solvency"}},
		{{Text: "Data Status", Data: "status"}},
	}}
}
