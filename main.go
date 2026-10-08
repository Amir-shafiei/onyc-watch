package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
func run() error {
	check := flag.Bool("check", false, "Fetch all live sources once and print JSON; no Telegram token needed")
	preview := flag.Bool("preview", false, "Fetch live sources and print the English bot messages; no token needed")
	solvencyCheck := flag.Bool("solvency-check", false, "Fetch and render live OnRe solvency data; no Telegram messages")
	priceCheck := flag.Bool("price-check", false, "Fetch official ONyc NAV without sending Telegram messages")
	flag.Parse()
	c, err := loadConfig()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	api := newAPI()
	if *priceCheck {
		child, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		v, err := fetchPrice(child, api.Client, onrePriceURL)
		if err != nil {
			return err
		}
		b := &Bot{Config: c}
		b.publishPrice(v, nil, time.Now())
		fmt.Println(b.priceText(time.Now()))
		return nil
	}
	if *solvencyCheck {
		child, cancel := context.WithTimeout(ctx, 25*time.Second)
		defer cancel()
		d, err := fetchSolvency(child, api.Client, solvencyURL)
		if err != nil {
			return err
		}
		b := &Bot{}
		b.solvency.publish(d, nil, time.Now())
		for _, section := range []string{"overview", "reserves", "allocation", "sources"} {
			fmt.Println(b.solvencyText(section, time.Now()))
		}
		return nil
	}
	if *check || *preview {
		s := api.collect(ctx)
		if *check {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(s); err != nil {
				return err
			}
		} else {
			u := newUser(1)
			fmt.Println(yieldText(s, u, c, time.Now()))
			fmt.Println()
			fmt.Println(borrowText(s, u, c, time.Now()))
			fmt.Println()
			fmt.Println(healthText(s))
		}
		for _, st := range s.Sources {
			if !st.OK {
				return fmt.Errorf("one or more providers failed; inspect source status")
			}
		}
		return nil
	}
	if c.Token == "" {
		return fmt.Errorf("set TELEGRAM_BOT_TOKEN in .env first; use -check or -preview to test live market data without a token")
	}
	if err := os.MkdirAll(c.DataDir, 0700); err != nil {
		return err
	}
	lock := filepath.Join(c.DataDir, "bot.lock")
	release, err := lockState(lock)
	if err != nil {
		return fmt.Errorf("cannot acquire bot lock (another instance may be running): %w", err)
	}
	defer release()
	state, err := loadState(c.DataDir)
	if err != nil {
		return err
	}
	tg := &Telegram{Client: &http.Client{Timeout: 35 * time.Second}, Base: "https://api.telegram.org/bot" + c.Token}
	bot := &Bot{Config: c, API: api, TG: tg, State: state}
	return bot.run(ctx)
}
