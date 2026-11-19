# Agent notes

This repository is the GitHub Actions runner scale-set Go client. A standalone
crypto price dashboard lives in `examples/cryptoprices`.

## Crypto price dashboard

- `examples/cryptoprices` is its **own Go module** (`go.mod`). That keeps it out
  of the root module's `./...` runs (`script/test`, `script/lint`), so the
  library's CI is untouched. It uses only the standard library — nothing to
  install.
- Run: `docker compose -f docker-compose.base44.yml up -d --build`, then
  http://localhost:3000. Health: `GET /healthz`. Data: `GET /api/prices`.
- `web/` is embedded into the binary with `go:embed`; the server serves
  `index.html` at `/` plus `style.css` and `app.js`.
- Live reload comes from `air` (installed in `examples/cryptoprices/Dockerfile`,
  configured by `.air.toml`). The repo is bind-mounted at `/app`; edits to Go,
  HTML, CSS or JS rebuild the server automatically.
- Prices come from CoinGecko's public `/api/v3/coins/markets` endpoint, which is
  rate limited. The server caches one snapshot for `REFRESH_SECONDS` (default
  60) and keeps the last good prices when a refresh fails, so don't poll the
  upstream directly from the browser. `COINGECKO_API_KEY` optionally sends a
  demo key; the app works without it.
- Coins are configured with the `COINS` env var (comma-separated CoinGecko ids);
  the server ranks them by market cap.
- Verify: `curl -s localhost:3000/healthz` returns `ok` and
  `curl -s localhost:3000/api/prices` returns `coins` with `price`/`change`
  strings.
