# Supervisor Example

This example runs a GitHub Actions runner scale set and serves a live view of
what it is doing. It uses the `supervisor` package, which implements
`listener.Scaler` on top of the `github.com/actions/scaleset` client.

> [!WARNING]
> This is a simplified example meant for demonstration and learning purposes. It is not intended for production use.

## What it does

1. Reuses the runner scale set with the given name, or creates it in the runner group.
2. Opens a message session and polls it for scale set messages.
3. Acquires the jobs GitHub offers.
4. Provisions a runner (a JIT config) for every job that has no runner yet, up to `--max-runners`, and keeps `--min-runners` around while there is no demand.
5. Retires runners that have no job when demand drops.
6. Serves the live view on `--addr`: desired versus active runners, each runner's state, the jobs it picked up and every scale decision.

The encoded JIT config is the new runner's credential. With `--runner-command`
the example starts a runner for each config, with `$RUNNER_NAME` and
`$RUNNER_JIT_CONFIG` in its environment; without it the runners are tracked but
nothing is started, which is what a scale set with an external launcher (or the
sandbox stand-in for GitHub's API) needs.

## Getting started

You'll need:

- A URL for the target repository, organization, or enterprise where the scale set is registered.
- [Credentials that have access to the above target](https://docs.github.com/en/actions/tutorials/use-actions-runner-controller/authenticate-to-the-api): a GitHub App (recommended) or a Personal Access Token.
- A name for the scale set, unique within its runner group.

```bash
go run ./examples/supervisor \
  --url https://github.com/my-org \
  --name my-scale-set \
  --token "$GITHUB_TOKEN" \
  --min-runners 1 \
  --max-runners 4
```

Then open <http://localhost:3000/>.

With a GitHub App instead of a token:

```bash
go run ./examples/supervisor \
  --url https://github.com/my-org \
  --name my-scale-set \
  --app-client-id Iv1.abc123 \
  --app-installation-id 12345678 \
  --app-private-key-file ./app.pem
```

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--url` | | **Required.** URL where the scale set is registered |
| `--name` | | **Required.** Name of the runner scale set |
| `--runner-group` | `default` | Runner group the scale set belongs to |
| `--min-runners` | `0` | Runners kept while there is no demand |
| `--max-runners` | `4` | Maximum runners, reported to GitHub as the capacity |
| `--addr` | `:3000` | Address the live view listens on |
| `--runner-command` | | Command that starts a runner; `$RUNNER_NAME` and `$RUNNER_JIT_CONFIG` are set |
| `--log-level` | `info` | `debug`, `info`, `warn` or `error` |
| `--token` | | Personal access token |
| `--app-client-id` | | GitHub App client id |
| `--app-installation-id` | | GitHub App installation id |
| `--app-private-key-file` | | Path to the GitHub App private key |

The message session is deleted on shutdown; the scale set is left in place so
the next run picks it up again.
