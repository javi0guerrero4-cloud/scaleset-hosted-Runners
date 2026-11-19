# Working on this repository in the Base44 sandbox

## What this repository is

`github.com/actions/scaleset` is a **Go client library** for the GitHub Actions
Runner Scale Set APIs, plus a `listener` package and one runnable example
(`examples/dockerscaleset`, a CLI that provisions runners as Docker containers).
It has no web application and no database, so there is nothing to "start" in the
usual sense.

## Preview

```bash
docker compose -f docker-compose.base44.yml up -d --build
```

This serves <http://localhost:3000/> from `hack/base44preview`. Because the
library has no UI of its own, that harness renders one page which drives the
**real** client through a complete flow — registration token → Actions tenant →
create scale set → message session → poll job message → acquire job → generate
JIT runner config → acknowledge → delete session and scale set — and prints the
payload of every call.

GitHub itself is not contacted. `hack/base44preview/stub.go` implements the
handful of GitHub REST and Actions service endpoints the flow needs, and the
client is pointed at it with a GitHub Enterprise Server style config URL
(`http://127.0.0.1:8081/myorg`), which is why the endpoints are served under
`/api/v3`. The stub keeps its state in memory and is reset on every page load.
Consequently the preview needs **no credentials** and creates nothing on GitHub.

Notes:

- Go has no hot reload. After editing Go code run
  `docker compose -f docker-compose.base44.yml restart preview` and reload the
  preview; otherwise the running binary stays as it was.
- Module downloads and build artifacts are kept in the `go-mod-cache` and
  `go-build-cache` volumes. The first boot downloads modules, so give it a
  couple of minutes before the page answers.
- The toolchain must satisfy the `go` directive in `go.mod` (currently
  `go 1.27.3`); the compose service uses `golang:1.27-bookworm` (go1.27.3).
  The official Go images set `GOTOOLCHAIN=local`, so an image older than the
  `go` directive does not fall back to downloading a toolchain — it fails at
  startup with `go.mod requires go >= X (running go Y; GOTOOLCHAIN=local)`.
  Bump the image tag whenever the `go` directive moves ahead of it (a
  dependency-bump commit such as "Bump go and go dependencies" is the usual
  cause of a preview that suddenly will not start).
- No sandbox-only application overrides are needed: the harness is a plain Go
  HTTP server that binds `:3000` and performs no host or origin checking, so no
  allowlists had to be widened and `BASE44_PREVIEW_MODE` is unused.

## Secrets and environment variables

The preview needs **no credentials**: the flow runs against the in-process
stand-in, so nothing is created on GitHub and no secret is required to boot.
`.base44/environment.json` therefore declares an empty `secrets` list.

Values configured in the dashboard are delivered to `/run/base44/app.env`
(outside the repo, never committed) and reach the preview process through the
`preview` service's `env_file:` entry. That entry is `required: false`, so the
stack still boots on a sandbox where no secrets have been set.

The only environment variables the code reads are optional:

- `GITHUB_ACTIONS_FORCE_GHES` — forces the GitHub Enterprise Server code path.
- `E2E` and the `E2E_SCALESET_*` variables — inputs for the opt-in live e2e
  suite (`examples/dockerscaleset/e2e_test.go`), which talks to a real GitHub
  target and is not run from this sandbox.

## Tests

The suite is the project's own `script/test` (`go test ./...`), which CI runs
with `-race`:

```bash
docker compose -f docker-compose.base44.yml run --rm preview go test ./... -race
```

Linting (`script/lint`) additionally needs `golangci-lint` and `deadcode` as Go
tools, which are fetched on first use.

## The example

`examples/dockerscaleset` is a worker, not a server, so it cannot be previewed:
it needs a Docker daemon, a real GitHub target and GitHub App credentials or a
PAT, and it registers a **real** runner scale set that GitHub may schedule jobs
onto. Do not start it from this sandbox unless that is explicitly wanted.
