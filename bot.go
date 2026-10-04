package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
)

var products = []struct{ key, label string }{{"yt-onyc", "YT ONyc"}, {"yt-sronyc", "YT srONyc"}, {"sronyc", "srONyc"}, {"jronyc", "jrONyc"}}

type Bot struct {
	Config    Config
	API       *API
	TG        *Telegram
	State     *State
	Cache     *MarketCache
	callbacks sync.WaitGroup
	ackSlots  chan struct{}
	delivery  chan alertDelivery
	sending   bool
	nextAlert time.Time
}

func mark(b bool) string {
	if b {
		return "✅ "
	}
	return "⬜ "
}
func keyHash(s string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(s)))[:16] }
func marketKeyboard(u *User, s Snapshot) *Keyboard {
	k := &Keyboard{}
	for _, p := range products {
		k.Rows = append(k.Rows, []Button{{Text: mark(u.Products[p.key]) + p.label, Data: "product:" + p.key}})
	}
	for _, p := range []string{"Kamino", "Loopscale"} {
		k.Rows = append(k.Rows, []Button{{Text: mark(u.Platforms[p]) + p + " USDC alerts", Data: "platform:" + p}})
	}
	for _, m := range s.Yields {
		k.Rows = append(k.Rows, []Button{{Text: mark(!u.Muted[m.ID]) + yieldTitle(m), Data: "market:" + keyHash(m.ID)}})
	}
	for _, m := range s.Borrows {
		k.Rows = append(k.Rows, []Button{{Text: mark(!u.Muted[m.ID]) + m.Platform + " " + m.Name + " " + m.Term, Data: "market:" + keyHash(m.ID)}})
	}
	k.Rows = append(k.Rows, []Button{{Text: "← Main Menu", Data: "home"}})
	return k
}
func apyMarketKeyboard(s Snapshot, mode string) *Keyboard {
	k := &Keyboard{}
	for _, m := range s.Yields {
		if mature(m.Maturity, time.Now()) {
			continue
		}
		k.Rows = append(k.Rows, []Button{{Text: yieldTitle(m) + " · " + rate(m.APY), Data: "pickapy:" + mode + ":" + keyHash(m.ID)}})
	}
	k.Rows = append(k.Rows, []Button{{Text: "Cancel", Data: "cancelsetting"}})
	return k
}
func alertSwitch(label, key string, disabled bool) Button {
	if disabled {
		return Button{Text: "Enable " + label + " Alerts", Data: "alerts:" + key + ":enable"}
	}
	return Button{Text: "Disable " + label + " Alerts", Data: "alerts:" + key + ":disable"}
}
func settingsKeyboard(u *User) *Keyboard {
	return &Keyboard{Rows: [][]Button{
		{{Text: "APY Alert", Data: "apyalert"}},
		{alertSwitch("USDC", "usdc", u.USDCAlertsDisabled)},
		{{Text: "Set Minimum USDC", Data: "set:amount"}, {Text: "Set Max Borrow APY", Data: "set:borrow"}},
		{{Text: "← Main Menu", Data: "home"}},
	}}
}
func apyAlertKeyboard(u *User) *Keyboard {
	toggle := alertSwitch("APY", "apy", u.APYAlertsDisabled)
	toggle.Text = "Disable"
	if u.APYAlertsDisabled {
		toggle.Text = "Enable"
	}
	return &Keyboard{Rows: [][]Button{
		{{Text: "Edit", Data: "apyalert:edit"}, toggle},
		{{Text: "Back to Settings", Data: "settings"}},
	}}
}
func (b *Bot) syncCache() {
	if b.Cache != nil {
		b.State.Snapshot = b.Cache.snapshot()
	}
}
func validSetting(text string, min, max float64) (float64, error) {
	n, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < min || n > max {
		return 0, fmt.Errorf("Enter a number between %g and %g, or /cancel.", min, max)
	}
	return n, nil
}
func applySetting(u *User, text string, currentAPY ...*float64) error {
	text = strings.TrimSpace(text)
	off := strings.EqualFold(text, "off")
	setting := u.Pending
	if (setting == "apy" || setting == "change") && u.PendingMarketID == "" {
		return fmt.Errorf("Choose an APY market first in Alert Settings.")
	}
	switch u.Pending {
	case "apy", "borrow":
		var p *float64
		if !off {
			v, e := validSetting(text, 0, 10000)
			if e != nil {
				return e
			}
			p = &v
		}
		if u.Pending == "apy" {
			if p == nil {
				return fmt.Errorf("Enter a numeric target APY, or /cancel.")
			}
			if len(currentAPY) == 0 || currentAPY[0] == nil {
				return fmt.Errorf("A fresh current APY is required. Try again after Refresh, or /cancel.")
			}
			u.APYDirection = "up"
			if *p < *currentAPY[0] {
				u.APYDirection = "down"
			}
			u.APYThreshold = p
		} else {
			u.MaxBorrowAPY = p
		}
	case "change":
		v, e := validSetting(text, 0.01, 1000)
		if e != nil {
			return e
		}
		u.APYChange = v
		u.APYThreshold = nil
		u.APYDirection = ""
	case "amount":
		v, e := validSetting(text, 0.01, 1e9)
		if e != nil {
			return e
		}
		u.MinUSDC = v
	default:
		return fmt.Errorf("No setting is awaiting input.")
	}
	if setting == "apy" || setting == "change" {
		u.APYMarketID, u.APYMarketName = u.PendingMarketID, u.PendingMarketName
		u.APYAlertsDisabled = false
		u.APYRevision++
		// The explicit selection also enables this market in My Markets.
		u.Muted[u.APYMarketID] = false
		delete(u.Alerts, "apy:"+u.APYMarketID)
	} else {
		// A USDC settings edit must not rearm APY alerts.
		for key := range u.Alerts {
			if strings.HasPrefix(key, "usdc:") {
				delete(u.Alerts, key)
			}
		}
	}
	u.Pending = ""
	u.PendingMarketID, u.PendingMarketName = "", ""
	return nil
}
func (b *Bot) handle(ctx context.Context, up Update) error {
	b.syncCache()
	msg := up.Message
	action := ""
	userID := int64(0)
	if up.Callback != nil {
		msg = up.Callback.Message
		userID = up.Callback.From.ID
		action = up.Callback.Data
		b.acknowledge(ctx, up.Callback.ID)
	} else if msg != nil {
		userID = msg.From.ID
	}
	if msg == nil || msg.Chat.Type != "private" {
		return nil
	}
	if len(b.Config.Allowed) > 0 && !b.Config.Allowed[userID] {
		return b.TG.send(ctx, msg.Chat.ID, "This bot is private. Contact its owner for access.", nil)
	}
	id := msg.Chat.ID
	u := b.State.Users[id]
	if u == nil {
		u = newUser(id)
		b.State.Users[id] = u
	}
	text := strings.TrimSpace(msg.Text)
	if action == "" {
		cmd := strings.Fields(text)
		if len(cmd) > 0 {
			cmd[0] = strings.Split(cmd[0], "@")[0]
		} else {
			return nil
		}
		switch cmd[0] {
		case "/start", "/help":
			action = "home"
		case "/apy":
			action = "apy"
		case "/usdc":
			action = "usdc"
		case "/settings":
			action = "settings"
		case "/markets":
			action = "markets"
		case "/status":
			action = "status"
		case "/refresh":
			action = "refresh"
		case "/stop":
			u.Paused = true
			action = "settings"
		case "/resume":
			u.Paused = false
			action = "settings"
		case "/cancel":
			u.Pending, u.PendingMarketID, u.PendingMarketName = "", "", ""
			action = "settings"
		case "/delete":
			delete(b.State.Users, id)
			return b.TG.send(ctx, id, "Your settings and alert history have been deleted. Send /start to subscribe again.", nil)
		default:
			if u.Pending != "" {
				apySetting := u.Pending == "apy" || u.Pending == "change"
				var currentAPY *float64
				for _, m := range b.State.Snapshot.Yields {
					if m.ID == u.PendingMarketID && yieldUsable(b.State.Snapshot, m, time.Now(), b.Config.StaleAfter) {
						currentAPY = m.APY
					}
				}
				if err := applySetting(u, text, currentAPY); err != nil {
					return b.TG.send(ctx, id, err.Error(), nil)
				}
				for _, m := range b.State.Snapshot.Yields {
					if m.ID == u.APYMarketID {
						u.Products[m.Product] = true
					}
				}
				action = "settings"
				if apySetting {
					action = "apyalert"
				}
			} else {
				return b.TG.send(ctx, id, "Use the buttons below or /help.", homeKeyboard())
			}
		}
	}
	if action == "apyalert" {
		u.Pending, u.PendingMarketID, u.PendingMarketName = "", "", ""
		if u.APYMarketID == "" {
			action = "set:apy"
		} else {
			return b.TG.send(ctx, id, apyAlertText(u, b.State.Snapshot), apyAlertKeyboard(u))
		}
	}
	if action == "apyalert:edit" {
		u.Pending, u.PendingMarketID, u.PendingMarketName = "", "", ""
		return b.TG.send(ctx, id, "Choose how to track your APY market. Your current alert stays unchanged until you save.", &Keyboard{Rows: [][]Button{
			{{Text: "Threshold", Data: "set:apy"}, {Text: "APY Change", Data: "set:change"}},
			{{Text: "Cancel", Data: "apyalert"}},
		}})
	}
	if action == "set:apy" || action == "set:change" || (action == "alerts:apy:enable" && u.APYMarketID == "") {
		mode := "apy"
		if action == "set:change" {
			mode = "change"
		}
		u.Pending, u.PendingMarketID, u.PendingMarketName = "", "", ""
		return b.TG.send(ctx, id, "Choose the market for your APY alert. Saving a new selection replaces your previous APY alert; USDC alerts are separate.", apyMarketKeyboard(b.State.Snapshot, mode))
	}
	if strings.HasPrefix(action, "pickapy:") {
		parts := strings.Split(action, ":")
		if len(parts) != 3 || (parts[1] != "apy" && parts[1] != "change") {
			return nil
		}
		for _, m := range b.State.Snapshot.Yields {
			if keyHash(m.ID) != parts[2] || mature(m.Maturity, time.Now()) {
				continue
			}
			u.Pending, u.PendingMarketID, u.PendingMarketName = parts[1], m.ID, yieldTitle(m)
			prompt := "Enter your target APY in percent, e.g. 12.9. A target below the current rate waits for a fall to that value or lower; a target above waits for a rise to that value or higher. The alert is removed after successful delivery."
			if parts[1] == "change" {
				prompt = "Enter the APY change in percentage points, e.g. 1. The current rate becomes the starting baseline. The alert is removed after successful delivery."
			}
			return b.TG.send(ctx, id, "Selected market: "+yieldTitle(m)+"\n"+m.Metric+"\n\n"+prompt+"\nSend /cancel to keep your previous alert.", nil)
		}
		return b.TG.send(ctx, id, "This market is no longer available. Choose a current market in Alert Settings.", settingsKeyboard(u))
	}
	if strings.HasPrefix(action, "set:") {
		u.Pending = strings.TrimPrefix(action, "set:")
		prompt := ""
		switch u.Pending {
		case "apy":
			prompt = "Enter an APY threshold in percent, e.g. 12.5, or off. For YT this tracks the market implied APY, not your realized YT return."
		case "change":
			prompt = "Enter the APY change in percentage points, e.g. 1. This enables change alerts and turns off the threshold."
		case "amount":
			prompt = "Enter the minimum reported USDC capacity, e.g. 1000."
		case "borrow":
			prompt = "Enter the maximum borrow APY in percent, e.g. 9, or off. Loopscale requires a fresh loan quote covering your minimum USDC amount at or below this rate."
		default:
			u.Pending = ""
			return nil
		}
		return b.TG.send(ctx, id, prompt+"\nSend /cancel to cancel.", nil)
	}
	if strings.HasPrefix(action, "product:") {
		key := strings.TrimPrefix(action, "product:")
		if _, ok := u.Products[key]; ok {
			u.Products[key] = !u.Products[key]
		}
		action = "markets"
	}
	if strings.HasPrefix(action, "platform:") {
		key := strings.TrimPrefix(action, "platform:")
		if _, ok := u.Platforms[key]; ok {
			u.Platforms[key] = !u.Platforms[key]
		}
		action = "markets"
	}
	if strings.HasPrefix(action, "market:") {
		hash := strings.TrimPrefix(action, "market:")
		for _, m := range b.State.Snapshot.Yields {
			if keyHash(m.ID) == hash {
				u.Muted[m.ID] = !u.Muted[m.ID]
			}
		}
		for _, m := range b.State.Snapshot.Borrows {
			if keyHash(m.ID) == hash {
				u.Muted[m.ID] = !u.Muted[m.ID]
			}
		}
		action = "markets"
	}
	switch action {
	case "alerts:apy:disable", "alerts:apy:enable", "alerts:usdc:disable", "alerts:usdc:enable":
		disabled := strings.HasSuffix(action, ":disable")
		if strings.HasPrefix(action, "alerts:apy:") {
			u.APYAlertsDisabled = disabled
			u.Pending, u.PendingMarketID, u.PendingMarketName = "", "", ""
			return b.TG.send(ctx, id, apyAlertText(u, b.State.Snapshot), apyAlertKeyboard(u))
		} else {
			u.USDCAlertsDisabled = disabled
		}
		u.Pending = ""
		return b.TG.send(ctx, id, settingsText(u), settingsKeyboard(u))
	case "cancelsetting":
		u.Pending, u.PendingMarketID, u.PendingMarketName = "", "", ""
		return b.TG.send(ctx, id, settingsText(u), settingsKeyboard(u))
	case "home":
		u.Pending = ""
		return b.TG.send(ctx, id, "ONyc Watch\n\nTrack Exponent YT ONyc, YT srONyc, srONyc and jrONyc rates, plus reported USDC capacity on Kamino and Loopscale.\n\nUse Current APY for the latest provider snapshot. Configure alerts and choose markets below. No wallet connection is needed.\n\n/stop pauses alerts · /resume resumes · /delete removes your saved settings.", homeKeyboard())
	case "apy":
		return b.TG.send(ctx, id, yieldText(b.State.Snapshot, u, b.Config, time.Now()), homeKeyboard())
	case "refresh":
		if b.Cache != nil {
			b.Cache.requestRefresh()
		}
		return b.TG.send(ctx, id, "Updating in the background. Current saved rates are below; tap Current APY again in a moment.\n\n"+yieldText(b.State.Snapshot, u, b.Config, time.Now()), homeKeyboard())
	case "usdc":
		return b.TG.send(ctx, id, borrowText(b.State.Snapshot, u, b.Config, time.Now()), homeKeyboard())
	case "markets":
		return b.TG.send(ctx, id, "⭐ My Markets\nToggle products, platforms or individual markets. Checked = watched. A product/platform must also be enabled for its market alerts.", marketKeyboard(u, b.State.Snapshot))
	case "pause":
		u.Paused = !u.Paused
		return b.TG.send(ctx, id, settingsText(u), settingsKeyboard(u))
	case "settings":
		return b.TG.send(ctx, id, settingsText(u), settingsKeyboard(u))
	case "status":
		return b.TG.send(ctx, id, healthText(b.State.Snapshot), homeKeyboard())
	default:
		return b.TG.send(ctx, id, "This button is no longer available. Use the main menu.", homeKeyboard())
	}
}

// Only the event loop accesses State. Workers receive immutable delivery payloads.
type alertDelivery struct {
	user        *User
	preferences string
	alert       Alert
	err         error
}

func preferences(u *User, key string) string {
	copy := *u
	copy.Alerts = nil
	copy.Pending = ""
	copy.PendingMarketID, copy.PendingMarketName = "", ""
	if strings.HasPrefix(key, "usdc:") {
		copy.APYMarketID, copy.APYMarketName = "", ""
		copy.APYDirection, copy.APYRevision = "", 0
		copy.APYThreshold, copy.APYChange, copy.APYAlertsDisabled, copy.Products = nil, 0, false, nil
	} else {
		copy.MinUSDC, copy.MaxBorrowAPY, copy.USDCAlertsDisabled, copy.Platforms = 0, nil, false, nil
	}
	marketID := strings.TrimPrefix(strings.TrimPrefix(key, "apy:"), "usdc:")
	copy.Muted = map[string]bool{marketID: u.Muted[marketID]}
	raw, _ := json.Marshal(copy)
	return string(raw)
}
func (b *Bot) acknowledge(ctx context.Context, id string) {
	if b.ackSlots == nil {
		b.ackSlots = make(chan struct{}, 16)
	}
	select {
	case b.ackSlots <- struct{}{}:
	default:
		return
	}
	b.callbacks.Add(1)
	go func() {
		defer b.callbacks.Done()
		defer func() { <-b.ackSlots }()
		ackCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		_ = b.TG.call(ackCtx, "answerCallbackQuery", map[string]any{"callback_query_id": id}, nil)
	}()
}
func (b *Bot) finishAlert(r alertDelivery) {
	b.sending = false
	b.nextAlert = time.Now().Add(1100 * time.Millisecond)
	u := b.State.Users[r.user.ChatID]
	if r.err != nil {
		var te *TelegramError
		if errors.As(r.err, &te) {
			if te.Code == 403 && u == r.user {
				u.Paused = true
			}
			if te.RetryAfter > 0 {
				b.nextAlert = time.Now().Add(time.Duration(te.RetryAfter) * time.Second)
			}
		} else {
			b.nextAlert = time.Now().Add(5 * time.Second)
		}
		log.Printf("Alert delivery failed: %v", r.err)
		return
	}
	if u != r.user || preferences(u, r.alert.Key) != r.preferences {
		return
	}
	// A delivered APY alert is one-shot, even if the market moved during delivery.
	if strings.HasPrefix(r.alert.Key, "apy:") {
		clearAPYAlert(u)
		return
	}
	// Recheck USDC eligibility so a changed condition is not incorrectly latched active.
	for _, a := range evaluateAlerts(u, b.State.Snapshot, time.Now(), b.Config) {
		if a.Key == r.alert.Key {
			u.Alerts[a.Key] = r.alert.NewState
			break
		}
	}
}
func (b *Bot) notify(ctx context.Context) {
	if b.sending || time.Now().Before(b.nextAlert) {
		return
	}
	if b.delivery == nil {
		b.delivery = make(chan alertDelivery, 1)
	}
	for _, u := range b.State.Users {
		alerts := evaluateAlerts(u, b.State.Snapshot, time.Now(), b.Config)
		if len(alerts) == 0 {
			continue
		}
		r := alertDelivery{user: u, preferences: preferences(u, alerts[0].Key), alert: alerts[0]}
		id := u.ChatID
		b.sending = true
		go func() {
			r.err = b.TG.send(ctx, id, r.alert.Text, homeKeyboard())
			select {
			case b.delivery <- r:
			case <-ctx.Done():
			}
		}()
		return
	}
}

type pollResult struct {
	updates []Update
	err     error
}

func (b *Bot) run(ctx context.Context) error {
	var me struct{ Username string }
	if err := b.TG.call(ctx, "getMe", map[string]any{}, &me); err != nil {
		return err
	}
	var wh struct{ URL string }
	if err := b.TG.call(ctx, "getWebhookInfo", map[string]any{}, &wh); err != nil {
		return err
	}
	if wh.URL != "" {
		return fmt.Errorf("bot has an active webhook; remove it before running this polling instance")
	}
	log.Printf("Starting @%s", me.Username)
	b.Cache = newMarketCache(b.State.Snapshot)
	cacheCtx, stopCache := context.WithCancel(ctx)
	defer stopCache()
	b.Cache.start(cacheCtx, b.API.jobs(), b.Config.PollInterval)
	b.delivery = make(chan alertDelivery, 1)
	// Poll independently of outgoing alerts; acknowledge offsets only after persistence.
	polls := make(chan pollResult)
	offsets := make(chan int64)
	go func(offset int64) {
		for cacheCtx.Err() == nil {
			ups, err := b.TG.updates(cacheCtx, offset)
			select {
			case polls <- pollResult{ups, err}:
			case <-cacheCtx.Done():
				return
			}
			select {
			case offset = <-offsets:
			case <-cacheCtx.Done():
				return
			}
		}
	}(b.State.Offset)
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	var retry <-chan time.Time
	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			b.syncCache()
			b.notify(cacheCtx)
		case r := <-b.delivery:
			b.syncCache()
			b.finishAlert(r)
			if err := b.State.save(b.Config.DataDir); err != nil {
				return err
			}
		case <-retry:
			retry = nil
			select {
			case offsets <- b.State.Offset:
			case <-ctx.Done():
				return nil
			}
		case p := <-polls:
			if p.err != nil {
				if ctx.Err() != nil {
					return nil
				}
				log.Printf("Polling: %v", p.err)
				delay := 5 * time.Second
				var te *TelegramError
				if errors.As(p.err, &te) {
					if te.Code == 401 || te.Code == 409 {
						return p.err
					}
					if te.RetryAfter > 0 {
						delay = time.Duration(te.RetryAfter) * time.Second
					}
				}
				retry = time.After(delay)
				continue
			}
			for _, up := range p.updates {
				if up.ID < b.State.Offset {
					continue
				}
				if err := b.handle(cacheCtx, up); err != nil {
					log.Printf("Command handling: %v", err)
				}
				b.State.Offset = up.ID + 1
				if err := b.State.save(b.Config.DataDir); err != nil {
					return err
				}
			}
			b.syncCache()
			if err := b.State.save(b.Config.DataDir); err != nil {
				return err
			}
			select {
			case offsets <- b.State.Offset:
			case <-ctx.Done():
				return nil
			}
		}
	}
	return nil
}
