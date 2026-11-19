package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// priceRecord is the raw numeric shape persisted for other tools to read, so
// they don't have to parse the dashboard's display strings.
type priceRecord struct {
	ID        string  `json:"id"`
	Symbol    string  `json:"symbol"`
	Name      string  `json:"name"`
	PriceUSD  float64 `json:"price_usd"`
	Change24h float64 `json:"change_24h_pct"`
	MarketCap float64 `json:"market_cap_usd"`
	Volume24h float64 `json:"volume_24h_usd"`
	High24h   float64 `json:"high_24h_usd"`
	Low24h    float64 `json:"low_24h_usd"`
}

type priceFile struct {
	UpdatedAt time.Time     `json:"updated_at"`
	Currency  string        `json:"currency"`
	Source    string        `json:"source"`
	Coins     []priceRecord `json:"coins"`
}

// writePriceFile atomically replaces path with the current snapshot.
func writePriceFile(path string, coins []marketCoin, now time.Time) error {
	sorted := sortByMarketCap(coins)
	records := make([]priceRecord, 0, len(sorted))
	for _, c := range sorted {
		change := 0.0
		if c.Change24h != nil {
			change = *c.Change24h
		}
		records = append(records, priceRecord{
			ID:        c.ID,
			Symbol:    strings.ToUpper(c.Symbol),
			Name:      c.Name,
			PriceUSD:  c.Price,
			Change24h: change,
			MarketCap: c.MarketCap,
			Volume24h: c.Volume,
			High24h:   c.High24h,
			Low24h:    c.Low24h,
		})
	}

	payload, err := json.MarshalIndent(priceFile{
		UpdatedAt: now.UTC(),
		Currency:  "usd",
		Source:    "coingecko",
		Coins:     records,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode price data: %w", err)
	}
	payload = append(payload, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".prices-*.json")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(payload); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return fmt.Errorf("set data file permissions: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace price data: %w", err)
	}
	return nil
}
