package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// IDs are monotonic and are never reused after deleting or completing a rule.
type WatchRule struct {
	ID, MarketID, Name, Kind, Direction string
	Target                              float64
	Disabled                            bool
	Revision                            uint64
	Maturity                            time.Time
	Sent7, Sent1                        bool
}

func ruleByID(u *User, id string) *WatchRule {
	for i := range u.Rules {
		if u.Rules[i].ID == id {
			return &u.Rules[i]
		}
	}
	return nil
}
func ruleDisplayNumber(u *User, id string) int {
	for i, r := range u.Rules {
		if r.ID == id {
			return i + 1
		}
	}
	return 0
}
func addRule(u *User, r WatchRule) {
	u.RuleSequence++
	r.ID = strconv.FormatUint(u.RuleSequence, 10)
	r.Revision = 1
	u.Rules = append(u.Rules, r)
}
func deleteRule(u *User, id string) {
	for i, r := range u.Rules {
		if r.ID == id {
			u.Rules = append(u.Rules[:i], u.Rules[i+1:]...)
			return
		}
	}
}
func migrateWatch(u *User) {
	if u.SeenMarkets == nil {
		u.SeenMarkets = map[string]bool{}
	}
	if u.SeededSources == nil {
		u.SeededSources = map[string]bool{}
	}
}
func watchPreferences(u *User, key string) string {
	if strings.HasPrefix(key, "new:") {
		return fmt.Sprintf("%t:%t", u.Paused, u.NewMarkets)
	}
	p := strings.Split(key, ":")
	if len(p) < 2 {
		return ""
	}
	r := ruleByID(u, p[1])
	if r == nil {
		return "deleted"
	}
	// Other rules and receipt flags must not invalidate this rule's delivery.
	c := *r
	c.Sent7 = false
	c.Sent1 = false
	raw, _ := json.Marshal(struct {
		Paused bool
		Rule   WatchRule
	}{u.Paused, c})
	return string(raw)
}
func evaluateWatch(u *User, s Snapshot, now time.Time, c Config) []Alert {
	migrateWatch(u)
	if u.Paused {
		return nil
	}
	var out []Alert
	for _, r := range u.Rules {
		if r.Disabled {
			continue
		}
		if r.Kind == "maturity" {
			remaining := r.Maturity.Sub(now)
			if remaining <= 0 {
				continue
			}
			stage := ""
			if remaining <= 24*time.Hour && !r.Sent1 {
				stage = "1"
			} else if remaining > 24*time.Hour && remaining <= 7*24*time.Hour && !r.Sent7 {
				stage = "7"
			}
			if stage != "" {
				out = append(out, Alert{Key: "watch:" + r.ID + ":" + stage, Text: fmt.Sprintf("Maturity reminder\n%s\nMatures: %s\nLess than %s day(s) remaining.\nThis is a market reminder, not confirmation of a wallet position.", r.Name, r.Maturity.UTC().Format("02 Jan 2006 15:04 UTC"), stage)})
			}
			continue
		}
		for _, y := range s.Yields {
			if y.ID != r.MarketID || !yieldUsable(s, y, now, c.StaleAfter) {
				continue
			}
			hit := r.Direction == "up" && *y.APY >= r.Target || r.Direction == "down" && *y.APY <= r.Target
			if hit {
				relation := "at or above"
				if r.Direction == "down" {
					relation = "at or below"
				}
				out = append(out, Alert{Key: "watch:" + r.ID + ":apy", Text: fmt.Sprintf("APY alert\n%s\n%s: %s\nTarget reached (%s): %.2f%%\n%s\nOne-time alert completed and removed.", yieldTitle(y), y.Metric, rate(y.APY), relation, r.Target, updated(y.FetchedAt))})
			}
		}
	}
	if !u.NewMarkets {
		return out
	}
	// Seed each provider independently, only after its first successful fresh response.
	for _, source := range s.Sources {
		if !source.OK || !isFresh(source.FetchedAt, now, c.StaleAfter) {
			continue
		}
		type entry struct{ id, name, url string }
		var entries []entry
		for _, y := range s.Yields {
			if yieldSource(y) == source.Name && !mature(y.Maturity, now) {
				entries = append(entries, entry{y.ID, yieldTitle(y), y.URL})
			}
		}
		for _, m := range s.Borrows {
			if m.Platform == source.Name {
				entries = append(entries, entry{m.ID, m.Name, m.URL})
			}
		}
		seeded := u.SeededSources[source.Name]
		for _, m := range entries {
			if !seeded {
				u.SeenMarkets[m.id] = true
				continue
			}
			if !u.SeenMarkets[m.id] {
				out = append(out, Alert{Key: "new:" + m.id, Text: "New market detected\n" + source.Name + " · " + m.name + "\nFirst seen by this bot; not necessarily the market launch time.\n" + m.url})
			}
		}
		u.SeededSources[source.Name] = true
	}
	return out
}
func finishWatch(u *User, a Alert) bool {
	if strings.HasPrefix(a.Key, "new:") {
		u.SeenMarkets[strings.TrimPrefix(a.Key, "new:")] = true
		return true
	}
	if !strings.HasPrefix(a.Key, "watch:") {
		return false
	}
	p := strings.Split(a.Key, ":")
	if len(p) != 3 {
		return true
	}
	r := ruleByID(u, p[1])
	if r == nil {
		return true
	}
	switch p[2] {
	case "apy":
		deleteRule(u, r.ID)
	case "7":
		r.Sent7 = true
	case "1":
		r.Sent1 = true
		r.Sent7 = true
	}
	return true
}
func watchList(u *User) (string, *Keyboard) {
	text := "My Alerts\nAPY targets are inclusive and removed after successful delivery.\n"
	if u.Paused {
		text += "All automatic notifications are paused.\n"
	}
	k := &Keyboard{Rows: [][]Button{{{Text: "Add APY Alert", Data: "wadd"}}}}
	if u.APYMarketID != "" {
		text += "\nExisting v1.7 alert: " + u.APYMarketName + "\n"
		k.Rows = append(k.Rows, []Button{{Text: "Manage existing v1.7 alert", Data: "apyalert"}})
	}
	for _, r := range u.Rules {
		condition := fmt.Sprintf("Above or equal %.2f%%", r.Target)
		if r.Direction == "down" {
			condition = fmt.Sprintf("Below or equal %.2f%%", r.Target)
		}
		if r.Kind == "maturity" {
			condition = "Maturity: " + r.Maturity.UTC().Format("02 Jan 2006")
		}
		state := "Enabled"
		if r.Disabled {
			state = "Paused"
		}
		if r.Kind == "maturity" && !r.Maturity.After(time.Now()) {
			state = "Expired"
		}
		number := ruleDisplayNumber(u, r.ID)
		text += fmt.Sprintf("\n#%d %s\n%s · %s\n", number, r.Name, condition, state)
		k.Rows = append(k.Rows, []Button{{Text: fmt.Sprintf("Manage #%d", number), Data: "wview:" + r.ID}})
	}
	if len(u.Rules) == 0 {
		text += "\nNo saved alerts."
	}
	k.Rows = append(k.Rows, []Button{{Text: "Main Menu", Data: "home"}})
	return text, k
}
func (b *Bot) handleWatch(ctx context.Context, u *User, action, text string) (bool, error) {
	migrateWatch(u)
	if action == "" {
		switch strings.Split(text, " ")[0] {
		case "/aum":
			action = "aum"
		case "/alerts":
			action = "watch"
		case "/maturities":
			action = "maturities"
		case "/newmarkets":
			action = "newmarkets"
		}
	}
	send := func(t string, k *Keyboard) (bool, error) { return true, b.TG.send(ctx, u.ChatID, t, k) }
	list := func() (bool, error) { t, k := watchList(u); return send(t, k) }
	if action == "aum" {
		return send(b.aumText(), homeKeyboard())
	}
	if action == "watch" {
		u.Pending = ""
		u.RuleEdit = ""
		return list()
	}
	if action == "wadd" || action == "maturities" {
		if len(u.Rules) >= 30 {
			return send("You can save up to 30 alerts. Remove an alert first.", homeKeyboard())
		}
		u.Pending = ""
		u.RuleEdit = ""
		k := &Keyboard{}
		prefix := "wpick:"
		title := "Choose an APY market."
		if action == "maturities" {
			prefix = "mpick:"
			title = "Choose a market for reminders 7 days and 1 day before maturity."
		}
		now := time.Now()
		for _, m := range b.State.Snapshot.Yields {
			if mature(m.Maturity, now) || (action == "maturities" && m.Maturity.IsZero()) {
				continue
			}
			k.Rows = append(k.Rows, []Button{{Text: yieldTitle(m), Data: prefix + keyHash(m.ID)}})
		}
		if len(k.Rows) == 0 {
			title = "No eligible markets are available yet. Try Refresh."
		}
		k.Rows = append(k.Rows, []Button{{Text: "Cancel", Data: "watch"}})
		return send(title, k)
	}
	if strings.HasPrefix(action, "wpick:") || strings.HasPrefix(action, "mpick:") {
		if len(u.Rules) >= 30 {
			return send("Alert limit reached.", homeKeyboard())
		}
		for _, m := range b.State.Snapshot.Yields {
			if keyHash(m.ID) != strings.SplitN(action, ":", 2)[1] || mature(m.Maturity, time.Now()) {
				continue
			}
			if strings.HasPrefix(action, "mpick:") {
				if m.Maturity.IsZero() {
					break
				}
				for _, r := range u.Rules {
					if r.Kind == "maturity" && r.MarketID == m.ID {
						return send("A reminder already exists for this market. Manage it in My Alerts.", homeKeyboard())
					}
				}
				addRule(u, WatchRule{Kind: "maturity", MarketID: m.ID, Name: yieldTitle(m), Maturity: m.Maturity})
				return list()
			}
			u.PendingMarketID = m.ID
			u.PendingMarketName = yieldTitle(m)
			u.Pending = "watchdirection"
			return send("Selected: "+yieldTitle(m)+"\n"+m.Metric+"\nChoose the condition (equality also triggers).", directionKeyboard())
		}
		return send("This market is no longer available.", homeKeyboard())
	}
	if strings.HasPrefix(action, "wdir:") {
		if u.Pending != "watchdirection" {
			return send("Select or edit an alert first.", homeKeyboard())
		}
		direction := strings.TrimPrefix(action, "wdir:")
		if direction != "up" && direction != "down" {
			return true, nil
		}
		u.RuleDirection = direction
		u.Pending = "watchtarget"
		return send("Enter target APY in percent, for example 12.95.\nSend /cancel to keep existing settings.", nil)
	}
	if strings.HasPrefix(action, "wview:") || strings.HasPrefix(action, "wedit:") || strings.HasPrefix(action, "wtoggle:") || strings.HasPrefix(action, "wdelete:") {
		p := strings.SplitN(action, ":", 2)
		r := ruleByID(u, p[1])
		if r == nil {
			return send("This alert was removed or completed.", homeKeyboard())
		}
		switch p[0] {
		case "wdelete":
			deleteRule(u, r.ID)
			u.Pending = ""
			return list()
		case "wtoggle":
			r.Disabled = !r.Disabled
			r.Revision++
			return list()
		case "wedit":
			if r.Kind != "apy" {
				return list()
			}
			u.RuleEdit = r.ID
			u.PendingMarketID = r.MarketID
			u.PendingMarketName = r.Name
			u.Pending = "watchdirection"
			return send("Choose the new condition. Changes apply after saving a target.", directionKeyboard())
		}
		toggle := "Pause"
		if r.Disabled {
			toggle = "Resume"
		}
		k := &Keyboard{Rows: [][]Button{{{Text: toggle, Data: "wtoggle:" + r.ID}, {Text: "Delete", Data: "wdelete:" + r.ID}}}}
		if r.Kind == "apy" {
			k.Rows = append(k.Rows, []Button{{Text: "Edit target / direction", Data: "wedit:" + r.ID}})
		}
		k.Rows = append(k.Rows, []Button{{Text: "Back", Data: "watch"}})
		return send(fmt.Sprintf("Manage #%d\n%s", ruleDisplayNumber(u, r.ID), r.Name), k)
	}
	if action == "newmarkets" || action == "newtoggle" {
		if action == "newtoggle" {
			u.NewMarkets = !u.NewMarkets
			u.SeenMarkets = map[string]bool{}
			u.SeededSources = map[string]bool{}
			if u.NewMarkets {
				copy := *u
				copy.Paused = false
				evaluateWatch(&copy, b.State.Snapshot, time.Now(), b.Config)
			}
		}
		label := "Enable"
		status := "Disabled"
		if u.NewMarkets {
			label = "Disable"
			status = "Enabled"
		}
		return send("New Market Alerts: "+status+"\nCovers discovered ONyc markets on Exponent, Kamino and Loopscale. Existing markets are silently recorded on the first successful refresh of each source.", &Keyboard{Rows: [][]Button{{{Text: label, Data: "newtoggle"}}, {{Text: "Main Menu", Data: "home"}}}})
	}
	if action == "" && u.Pending == "watchtarget" && !strings.HasPrefix(text, "/") {
		available := false
		for _, m := range b.State.Snapshot.Yields {
			if m.ID == u.PendingMarketID && !mature(m.Maturity, time.Now()) {
				available = true
			}
		}
		if !available {
			u.Pending = ""
			return send("Market unavailable or expired. Select a current market.", homeKeyboard())
		}
		v, err := validSetting(text, 0, 10000)
		if err != nil {
			return send(err.Error(), nil)
		}
		if u.RuleDirection != "up" && u.RuleDirection != "down" {
			return send("Choose Above or Below first.", directionKeyboard())
		}
		if u.RuleEdit != "" {
			r := ruleByID(u, u.RuleEdit)
			if r == nil {
				u.Pending = ""
				return send("The original alert has completed or was deleted. Add a new alert.", homeKeyboard())
			}
			r.Target = v
			r.Direction = u.RuleDirection
			r.Revision++
		} else {
			if len(u.Rules) >= 30 {
				return send("Alert limit reached.", homeKeyboard())
			}
			addRule(u, WatchRule{MarketID: u.PendingMarketID, Name: u.PendingMarketName, Kind: "apy", Target: v, Direction: u.RuleDirection})
		}
		u.Pending = ""
		u.RuleEdit = ""
		return list()
	}
	if action == "" && u.Pending == "watchdirection" && !strings.HasPrefix(text, "/") {
		return send("Choose Above or Below using the buttons.", directionKeyboard())
	}
	return false, nil
}
func directionKeyboard() *Keyboard {
	return &Keyboard{Rows: [][]Button{{{Text: "Above ↑", Data: "wdir:up"}, {Text: "Below ↓", Data: "wdir:down"}}, {{Text: "Cancel", Data: "watch"}}}}
}
