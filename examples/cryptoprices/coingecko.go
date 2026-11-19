package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// marketCoin mirrors the subset of CoinGecko's /coins/markets response the
// dashboard uses.
type marketCoin struct {
	ID        string   `json:"id"`
	Symbol    string   `json:"symbol"`
	Name      string   `json:"name"`
	Image     string   `json:"image"`
	Price     float64  `json:"current_price"`
	MarketCap float64  `json:"market_cap"`
	Volume    float64  `json:"total_volume"`
	High24h   float64  `json:"high_24h"`
	Low24h    float64  `json:"low_24h"`
	Change24h *float64 `json:"price_change_percentage_24h"`

	Sparkline struct {
		Price []float64 `json:"price"`
	} `json:"sparkline_in_7d"`
}

type priceSource struct {
	client  *http.Client
	baseURL string
	apiKey  string
}

func newPriceSource(baseURL, apiKey string) *priceSource {
	return &priceSource{
		client:  &http.Client{Timeout: 15 * time.Second},
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
	}
}

func (s *priceSource) Fetch(ctx context.Context, ids []string) ([]marketCoin, error) {
	q := url.Values{}
	q.Set("vs_currency", "usd")
	q.Set("ids", strings.Join(ids, ","))
	q.Set("order", "market_cap_desc")
	q.Set("per_page", "100")
	q.Set("page", "1")
	q.Set("sparkline", "true")
	q.Set("price_change_percentage", "24h")

	endpoint := s.baseURL + "/api/v3/coins/markets?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("accept", "application/json")
	if s.apiKey != "" {
		req.Header.Set("x-cg-demo-api-key", s.apiKey)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reach coingecko: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("read coingecko response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("coingecko responded %s: %s", resp.Status, snippet(body))
	}

	var coins []marketCoin
	if err := json.Unmarshal(body, &coins); err != nil {
		return nil, fmt.Errorf("decode coingecko response: %w", err)
	}
	if len(coins) == 0 {
		return nil, fmt.Errorf("coingecko returned no prices")
	}

	return coins, nil
}

func snippet(body []byte) string {
	const max = 180
	text := strings.TrimSpace(string(body))
	if len(text) > max {
		text = text[:max] + "…"
	}
	return text
}
