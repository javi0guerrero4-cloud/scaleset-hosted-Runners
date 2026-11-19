// Command cryptoprices serves a small live dashboard of cryptocurrency spot
// prices in USD, sourced from the CoinGecko public API.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

//go:embed web
var webFS embed.FS

const defaultCoins = "bitcoin,ethereum,solana,binancecoin,ripple,cardano,dogecoin,avalanche-2,chainlink,polkadot,litecoin,tron"

type config struct {
	port       string
	coinIDs    []string
	refresh    time.Duration
	apiBaseURL string
	apiKey     string
}

func loadConfig() config {
	cfg := config{
		port:       envOr("PORT", "3000"),
		refresh:    60 * time.Second,
		apiBaseURL: envOr("COINGECKO_API_URL", "https://api.coingecko.com"),
		apiKey:     os.Getenv("COINGECKO_API_KEY"),
	}

	if v := os.Getenv("REFRESH_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 10 {
			cfg.refresh = time.Duration(n) * time.Second
		}
	}

	for _, id := range strings.Split(envOr("COINS", defaultCoins), ",") {
		if id = strings.TrimSpace(id); id != "" {
			cfg.coinIDs = append(cfg.coinIDs, id)
		}
	}

	return cfg
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	cfg := loadConfig()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	st := newStore(cfg, newPriceSource(cfg.apiBaseURL, cfg.apiKey), logger)

	// Fetch once before serving so the first request has prices.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	if err := st.Refresh(ctx); err != nil {
		logger.Warn("initial price fetch failed", "error", err)
	}
	cancel()

	go st.Run(context.Background())

	web, err := fs.Sub(webFS, "web")
	if err != nil {
		logger.Error("embedded assets unavailable", "error", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("GET /api/prices", handlePrices(st))
	mux.Handle("GET /", noStore(http.FileServer(http.FS(web))))

	addr := ":" + cfg.port
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	logger.Info("serving crypto prices",
		"addr", addr,
		"coins", len(cfg.coinIDs),
		"refresh", cfg.refresh.String(),
	)

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

type apiResponse struct {
	UpdatedAt  string     `json:"updated_at,omitempty"`
	UpdatedAgo string     `json:"updated_ago,omitempty"`
	Error      string     `json:"error,omitempty"`
	Coins      []viewCoin `json:"coins"`
}

func handlePrices(st *store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		snap := st.Snapshot()
		if len(snap.Coins) == 0 || time.Since(snap.UpdatedAt) > st.ttl {
			ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
			_ = st.Refresh(ctx)
			cancel()
			snap = st.Snapshot()
		}

		resp := apiResponse{Coins: snap.Coins, Error: snap.Err}
		if resp.Coins == nil {
			resp.Coins = []viewCoin{}
		}
		if !snap.UpdatedAt.IsZero() {
			resp.UpdatedAt = snap.UpdatedAt.UTC().Format(time.RFC3339)
			resp.UpdatedAgo = humanAgo(time.Since(snap.UpdatedAt))
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func humanAgo(d time.Duration) string {
	switch {
	case d < 5*time.Second:
		return "just now"
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	default:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
}
