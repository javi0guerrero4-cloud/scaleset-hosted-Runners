# Working on this repository in the Base44 sandbox

## What this repository is

`github.com/actions/scaleset` is a **Go client library** for the GitHub Actions
Runner Scale Set APIs, plus a `listener` package, a `supervisor` package, and
runnable examples. It has no web application and no database.

## Preview

```bash
docker compose -f docker-compose.base44.yml up -d
```

<http://localhost:3000/> is the **supervisor example** (`examples/supervisor`)
with its live view: the scale set it supervises, the runners it provisioned,
the jobs it acquired and every scale decision it made. The page polls
`/api/state` once a second, so it moves on its own.

It talks to `hack/githubstub`, a stand-in for the GitHub REST API and the
Actions runner scale set service, which runs in its own container. GitHub is
never contacted, no credentials are needed, and nothing is created on GitHub.
The stand-in keeps state in memory, plays the scheduler that assigns jobs to
idle runners, and drives a repeating burst of demand (`-workload`): four jobs
arrive, two are canceled before a runner picks them up, the other two run for a
few seconds, then the scale set goes idle for the rest of the ~26s cycle. That
is what makes the live view show scale-up, retirement, busy runners and
finished runners.

Notes:

- Go has no hot reload. After editing Go code run
  `docker compose -f docker-compose.base44.yml restart supervisor` (or `stub`)
  and reload the preview.
- The first boot downloads modules into the `go-mod-cache` volume; give it a
  couple of minutes before the page answers.
- `go.mod` requires go 1.27.3 and the services use `golang:1.27-bookworm`
  (go1.27.4).
- The stand-in advertises `http://stub:8081` to the client (`-advertise`), which
  must stay the compose service name: the client is told to call that URL back
  for the Actions endpoints, and it is resolved from inside the supervisor
  container.
- Neither service checks Host or Origin, so no allowlists had to be widened and
  `BASE44_PREVIEW_MODE` is unused.

## Tests

The suite is the project's own `script/test` (`go test ./...`), which CI runs
with `-race`:

```bash
docker compose -f docker-compose.base44.yml exec -T supervisor go test ./...
# -race needs cgo, which the services disable by default:
docker compose -f docker-compose.base44.yml exec -T -e CGO_ENABLED=1 supervisor go test ./... -race
```

The `supervisor` package tests use a fake session and a fake provisioner, so
they need neither GitHub nor the stand-in.

## The examples

`examples/supervisor` is the command the preview runs. Pointed at a real
organization it creates a real runner scale set and generates real JIT runner
configs, so do not run it from this sandbox against GitHub. `--runner-command`
starts a runner per JIT config; without it runners are only tracked.

`examples/dockerscaleset` is a worker, not a server, so it cannot be previewed:
it needs a Docker daemon, a real GitHub target and GitHub App credentials or a
PAT.
