# Crypto price dashboard (examples/cryptoprices)

A small Go service that renders live cryptocurrency spot prices (USD) from the
CoinGecko public API. It is a separate Go module, so the library's
`go test ./...`, `golangci-lint` and `go tool deadcode` runs are unaffected and
there are no dependencies to install.

## Run it

```sh
docker compose -f docker-compose.base44.yml up -d --build
```

Open http://localhost:3000.

- `GET /` — dashboard (static assets embedded in the binary)
- `GET /api/prices` — current snapshot as JSON
- `GET /data/prices.json` — the saved price data file
- `GET /healthz` — liveness probe

## Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `PORT` | `3000` | HTTP listen port |
| `COINS` | 12 major coins | Comma-separated CoinGecko coin ids |
| `REFRESH_SECONDS` | `60` | Upstream cache TTL (minimum 10) |
| `COINGECKO_API_URL` | `https://api.coingecko.com` | API base URL |
| `COINGECKO_API_KEY` | unset | Optional demo API key (sent as `x-cg-demo-api-key`) |
| `DATA_FILE` | `data/prices.json` | Where each snapshot is saved |

The public API is rate limited (roughly 5–30 requests/minute). The server keeps
one cached snapshot and refreshes it at most once per `REFRESH_SECONDS`, so the
browser can poll freely. If a refresh fails, the last good prices stay on screen
and the error is reported in the response.

## Saved data

Every successful refresh atomically rewrites `DATA_FILE` with the raw numbers
for each coin, so other code can read prices without calling the API:

```json
{
  "updated_at": "2026-11-19T22:06:25Z",
  "currency": "usd",
  "source": "coingecko",
  "coins": [
    {
      "id": "bitcoin",
      "symbol": "BTC",
      "name": "Bitcoin",
      "price_usd": 61701,
      "change_24h_pct": -1.04,
      "market_cap_usd": 1238062344690.8,
      "volume_24h_usd": 32870000000,
      "high_24h_usd": 64006,
      "low_24h_usd": 61419
    }
  ]
}
```

The file is generated at runtime and git-ignored; it is also served at
`GET /data/prices.json`.
