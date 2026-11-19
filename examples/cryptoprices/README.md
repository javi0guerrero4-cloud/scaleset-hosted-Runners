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
- `GET /healthz` — liveness probe

## Configuration

| Variable | Default | Purpose |
| --- | --- | --- |
| `PORT` | `3000` | HTTP listen port |
| `COINS` | 12 major coins | Comma-separated CoinGecko coin ids |
| `REFRESH_SECONDS` | `60` | Upstream cache TTL (minimum 10) |
| `COINGECKO_API_URL` | `https://api.coingecko.com` | API base URL |
| `COINGECKO_API_KEY` | unset | Optional demo API key (sent as `x-cg-demo-api-key`) |

The public API is rate limited (roughly 5–30 requests/minute). The server keeps
one cached snapshot and refreshes it at most once per `REFRESH_SECONDS`, so the
browser can poll freely. If a refresh fails, the last good prices stay on screen
and the error is reported in the response.
