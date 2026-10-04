package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type User struct {
	Rules         []WatchRule
	RuleSequence  uint64
	RuleEdit      string
	RuleDirection string
	NewMarkets    bool
	SeenMarkets   map[string]bool
	SeededSources map[string]bool
	ChatID        int64
	Paused        bool
	// Disabled flags preserve existing subscriptions when loading older state files.
	APYAlertsDisabled  bool
	USDCAlertsDisabled bool
	Products           map[string]bool
	Platforms          map[string]bool
	// Empty map means all currently discovered markets; individual exclusions persist.
	Muted             map[string]bool
	APYMarketID       string
	APYMarketName     string
	PendingMarketID   string
	PendingMarketName string
	APYDirection      string
	APYRevision       uint64
	APYThreshold      *float64
	APYChange         float64
	MinUSDC           float64
	MaxBorrowAPY      *float64
	Pending           string
	Alerts            map[string]AlertState
}
type AlertState struct {
	Active    bool
	LastSent  time.Time
	LastValue *float64
}
type State struct {
	Version  int
	Offset   int64
	Users    map[int64]*User
	Snapshot Snapshot
}

func newUser(id int64) *User {
	return &User{ChatID: id, APYAlertsDisabled: true, USDCAlertsDisabled: true, Products: map[string]bool{"yt-onyc": true, "yt-sronyc": true, "sronyc": true, "jronyc": true}, Platforms: map[string]bool{"Kamino": true, "Loopscale": true}, Muted: map[string]bool{}, APYChange: 1, MinUSDC: 100, Alerts: map[string]AlertState{}}
}
func loadState(dir string) (*State, error) {
	s := &State{Version: 1, Users: map[int64]*User{}}
	b, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(b, s); err != nil {
		return nil, fmt.Errorf("state file is invalid; restore a backup before restarting: %w", err)
	}
	if s.Version != 1 || s.Users == nil {
		return nil, fmt.Errorf("unsupported state file")
	}
	for _, u := range s.Users {
		if u == nil || u.Products == nil || u.Platforms == nil || u.Alerts == nil || u.Muted == nil {
			return nil, fmt.Errorf("incomplete user state")
		}
		if u.APYMarketID != "" && u.Alerts["apy:"+u.APYMarketID].Active {
			clearAPYAlert(u)
		}
		if u.APYMarketID == "" {
			u.APYAlertsDisabled = true
			if u.Pending == "apy" || u.Pending == "change" {
				u.Pending = ""
			}
		}
	}
	return s, nil
}
func (s *State) save(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "state-*.tmp")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(name, filepath.Join(dir, "state.json"))
}
func yieldSource(y YieldMarket) string {
	if y.Product == "sronyc" || y.Product == "jronyc" {
		return "Exponent tranches"
	}
	return "Exponent yields"
}
func mergeSnapshot(old, next Snapshot) Snapshot {
	// Retain last known data only for failed sources, never for a successful empty response.
	for _, y := range old.Yields {
		if !next.sourceOK(yieldSource(y)) {
			next.Yields = append(next.Yields, y)
		}
	}
	for _, b := range old.Borrows {
		if !next.sourceOK(b.Platform) {
			next.Borrows = append(next.Borrows, b)
		}
	}
	return next
}

func clearAPYAlert(u *User) {
	delete(u.Alerts, "apy:"+u.APYMarketID)
	u.APYMarketID, u.APYMarketName, u.APYDirection = "", "", ""
	u.APYThreshold = nil
	u.APYAlertsDisabled = true
	u.APYRevision++
}
