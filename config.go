package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Token, DataDir                     string
	PollInterval, StaleAfter, Cooldown time.Duration
	Allowed                            map[int64]bool
}

func loadConfig() (Config, error) {
	// Environment variables take precedence over the local .env file.
	if f, err := os.Open(".env"); err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			k, v, ok := strings.Cut(line, "=")
			if !ok {
				return Config{}, fmt.Errorf("invalid .env line")
			}
			k = strings.TrimSpace(k)
			v = strings.Trim(strings.TrimSpace(v), "\"'")
			if _, exists := os.LookupEnv(k); !exists {
				if err := os.Setenv(k, v); err != nil {
					return Config{}, err
				}
			}
		}
		if err := sc.Err(); err != nil {
			return Config{}, err
		}
	} else if !os.IsNotExist(err) {
		return Config{}, err
	}
	c := Config{Token: os.Getenv("TELEGRAM_BOT_TOKEN"), DataDir: os.Getenv("DATA_DIR"), Allowed: map[int64]bool{}}
	if c.DataDir == "" {
		c.DataDir = "data"
	}
	for _, spec := range []struct {
		k, d   string
		target *time.Duration
	}{{"POLL_INTERVAL", "60s", &c.PollInterval}, {"STALE_AFTER", "10m", &c.StaleAfter}, {"ALERT_COOLDOWN", "15m", &c.Cooldown}} {
		value := os.Getenv(spec.k)
		if value == "" {
			value = spec.d
		}
		d, err := time.ParseDuration(value)
		if err != nil || d <= 0 {
			return c, fmt.Errorf("invalid %s", spec.k)
		}
		*spec.target = d
	}
	if c.PollInterval < 30*time.Second {
		return c, fmt.Errorf("POLL_INTERVAL must be at least 30s")
	}
	if c.StaleAfter < c.PollInterval {
		return c, fmt.Errorf("STALE_AFTER must be at least POLL_INTERVAL")
	}
	for _, s := range strings.Split(os.Getenv("ALLOWED_USER_IDS"), ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil || id <= 0 {
			return c, fmt.Errorf("invalid ALLOWED_USER_IDS")
		}
		c.Allowed[id] = true
	}
	return c, nil
}
