# RunWisp

A modern, open-source replacement for `crond` and `supervisord`. Schedule tasks with cron expressions, supervise long-running processes, stream logs in real time, and manage everything through a built-in web UI or REST API.

## Features

- Cron scheduling with catch-up behavior
- Queue, skip, or terminate concurrency policies
- Retries with configurable backoff
- Per-task log retention and overflow controls
- Built-in web UI and terminal UI
- Embedded SQLite for runs, logs, and state

## Install

Download the latest binary from [Releases](https://github.com/runwisp/runwisp/releases), or build from source from the repo root:

```bash
git clone https://github.com/runwisp/runwisp
cd runwisp
bun install
bun run build
bun run test
bun run check
```

The resulting `runwisp` binary is self-contained. Copy it anywhere in your `$PATH`.

## Quick Start

```bash
./runwisp init     # scaffold a runwisp.toml
./runwisp validate # sanity-check the config
./runwisp          # launch RunWisp and open the terminal UI
```

Tasks are stored in `runwisp.toml` — edit it with your editor. Full config example:

```toml
[storage]
max_size       = "5gb"
min_free_space = "500mb"

[defaults]
timeout      = "1h"
log_max_size = "100mb"
log_on_full  = "drop_old"
keep_runs    = 50
keep_for     = "30d"

[tasks.hello-world]
description = "Simple hello world task"
cron        = "*/5 * * * *"
run = """
echo "Hello, World!"
echo "Current date: $(date)"
"""
```

RunWisp listens on port 9477 by default. Open `http://localhost:9477` to access the web dashboard.

## API Reference

### Authentication

RunWisp uses challenge-response authentication so the password is never sent over the wire:

```bash
NONCE=$(curl -s http://localhost:9477/api/auth/challenge | jq -r '.nonce')

TOKEN=$(curl -s -X POST http://localhost:9477/api/auth \
  -H "Content-Type: application/json" \
  -d "{\"nonce\":\"$NONCE\",\"response\":\"$(echo -n "$PASSWORD:$NONCE" | sha256sum | cut -d' ' -f1)\"}" \
  | jq -r '.token')
```

Use the token in subsequent requests:

```bash
curl -H "Authorization: Bearer $TOKEN" http://localhost:9477/api/tasks
```

### Endpoints

- `GET /api/auth/challenge`: get a single-use login nonce
- `POST /api/auth`: exchange a challenge response for a JWT
- `GET /api/auth/status`: check authentication status
- `GET /api/openapi`: OpenAPI specification
- `GET /api/tasks`: list tasks
- `GET /api/runs`: list runs
- `GET /api/tasks/{task}/runs`: list runs for a task
- `POST /api/tasks/{task}/run`: trigger a task
- `GET /api/tasks/{task}/runs/{id}`: get run details
- `DELETE /api/tasks/{task}/runs/{id}`: delete a run
- `GET /api/tasks/{task}/runs/{id}/log`: fetch log output
- `GET /api/tasks/{task}/runs/{id}/log-stream`: stream logs

## Environment Variables

- `RUNWISP_PASSWORD`: login password. If unset, a random password is generated and shown in the TUI.
- `RUNWISP_TRUST_PROXY`: comma-separated CIDR ranges of trusted reverse proxies.

## Deployment

> Planned feature: `./runwisp service` sets up a systemd unit and starts the service.

> Planned feature: Docker image for containerized deployments.

## Security

- All API and web UI access is authenticated via challenge-response plus JWT
- Use HTTPS behind a reverse proxy in production
- Restrict access to the daemon port
- Run RunWisp under a dedicated low-privilege user

See [SECURITY.md](../../SECURITY.md) for vulnerability reporting.

## Development

See [CONTRIBUTING.md](../../CONTRIBUTING.md) for build instructions and project structure.
