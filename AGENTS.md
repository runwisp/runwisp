# RunWisp — Agent Directives

**License**: Apache-2.0 · **Status**: Pre-1.0 (breaking changes permitted)
**Stack**: Go 1.25 daemon, Svelte 5 (runes) + Tailwind UI, Bun workspaces + Make, embedded SQLite (`database/sql` + `modernc.org/sqlite`), AsyncAPI-defined optional control-plane protocol.

## 🎯 PRODUCT VISION (read this first — it outranks everything below)

RunWisp replaces **crond + supervisord** with one small Go binary that a single developer can drop on a VPS, a Raspberry Pi, or into a Docker image and immediately see *what ran, when, why it failed, and what it printed*.

**Prime directives** (in priority order; when they conflict, the higher one wins):

1. **Nothing silently fails.** Every run has an exit code, duration, timestamps, and captured output — persisted, browsable, and streamable. If a change makes failures invisible, reject it.
2. **One binary, zero runtime deps.** No Python, Node, external DB, systemd, or sidecars required to run RunWisp. SQLite and the web UI are *embedded*. Do not add runtime deps; prefer a vendored Go lib over a service.
3. **TOML is the sole source of truth.** `runwisp.toml` defines every task. The REST API and Web UI are **read-only + trigger** — they never mutate task definitions. Schema changes are user-visible breaking changes; treat the TOML surface as an API even pre-1.0. Never add a feature that *requires* the UI or API to configure.
4. **Local-first, offline-complete.** The daemon must work fully offline. Any network integration (`internal/cloud/`) is strictly optional — no feature may degrade when it's disabled or unreachable.
5. **Built for the individual and the small team.** Every core capability ships in the binary: scheduling, supervision, observability, web UI, TUI, REST. No artificial limits, no feature flags gating basics.
6. **Boring in prod.** Predictable resource use, graceful shutdown, recoverable state after crash or kill -9. Prefer a simple mechanism that's easy to reason about over a clever one that saves 5%.

## 🚫 NON-GOALS

- **DAGs / workflow orchestration** — that's Dagu/Airflow/Temporal. RunWisp tasks are independent units.
- **Clustering, leader election, HA failover, cross-instance coordination** — one daemon owns its tasks. Anything involving multiple daemons acting in concert is out of scope for the daemon itself; operators who want that can build it on top of the REST API / control-plane protocol.
- **Plugin systems / arbitrary extensibility** — the surface is TOML + shell commands + REST. No JS hooks, no Lua, no WASM.
- **Replacing the user's shell or package manager** — `run:` is a shell command the user already knows how to write. Don't invent a DSL on top of it.
- **Being a log aggregator** — we capture per-run stdout/stderr for visibility. We are not Loki, not ELK.
- **Enterprise identity systems** — CHAP + JWT answers "does this operator control this daemon?". SSO, directory integration, org/team modeling, fine-grained RBAC policies are outside the daemon's scope.
- **Long-horizon analytics / reporting** — retention is per-task and bounded. Anything that needs cross-task, cross-instance, or indefinite history lives outside the daemon.

When in doubt, ask: *"Does this help **one** operator run **their** tasks on **one** machine better?"* If no, it probably doesn't belong in `apps/runwisp`.

## 🧭 INVARIANTS (violating any of these is a bug, regardless of what a test says)

- **Supported platforms**: Linux, macOS, WSL. These are first-class — builds, tests, manual smoke. Native Windows is out of scope.
- **Config reload is restart-only.** A running daemon keeps its in-memory task set for its entire lifetime; to pick up TOML changes, the operator restarts the daemon. No file-watchers, no auto-reload, no SIGHUP handler, no `runwisp reload` command.
- **Crash safety**: Killing the daemon (SIGKILL, power loss) must not corrupt state. On restart, any run that was in-flight is marked **interrupted** with a terminal status — it is **not resumed**.
- **Determinism of scheduling**: Given the same TOML + clock, the scheduler produces the same firings. Randomness, wall-clock reads, and FS I/O are injected, never called inline in scheduling logic.
- **No required network**: Daemon startup, task execution, UI serving, and TUI must all work with the NIC unplugged. Any outbound integration attempts happen in the background and never block the hot path.
- **Single writer per task**: Exactly one goroutine/run-manager owns a task's run lifecycle. Any other code observing state does so via `internal/events/` or read-only storage queries.
- **Generated code is write-once**: `internal/generated/protocol/` is regenerated from `packages/asyncapi/asyncapi.yaml`. Never hand-edit. If you need a new message, edit the AsyncAPI spec.
- **Embedded assets stay embedded**: The web UI ships inside the binary. No "download assets at runtime", no CDN fallback.

## 🔐 TRUST MODEL

- The daemon runs **with the privilege of whoever started it**, executing **user-authored shell** from TOML. Therefore:
  - TOML is trusted input; the REST API / UI is not.
  - Never execute user-provided strings from HTTP/WS bodies as shell. `run =` comes from disk only.
  - Secrets (JWT secret, passwords) live under the data dir (`internal/datadir/`) with restrictive perms; never log them; never transmit them over any outbound integration.
- CHAP (challenge-response) auth is the login boundary. JWT is the session. Don't bypass either for "convenience" endpoints.

## 🧠 DECISION HEURISTICS (use when the spec is silent)

1. **Does it make a failure more visible?** → Yes = lean toward it.
2. **Does it add a runtime dependency or a required network call?** → Yes = reject or make it strictly optional.
3. **Does it add state that must survive restart?** → It goes through `internal/storage/` (`database/sql` + `modernc.org/sqlite`), gets a ULID, and has a reconciliation path on boot.
4. **Can a solo dev understand it by reading `runwisp.toml` + the web UI?** → If no, simplify or document.

## 🚨 TECHNICAL DEBT & HYGIENE (HIGHEST PRIORITY)

1. **Boy Scout Rule**: When modifying a file, fix adjacent violations (broken naming, dead code, leaked concerns, missing types).
2. **Aggressive Extraction**: Duplicated logic across 2+ files lands in the most specific shared location — `internal/<closest>`, `packages/common`, or a new `internal/` package if scope is wider than one caller.
3. **No Reinvention**: Before writing utilities (slug, cron parse, retry, ID gen), check `packages/common` and the Go standard library. For new deps, prefer a vendored Go lib with real usage over a hand-rolled abstraction.
4. **TypeScript-only rules** (apply in `apps/ui` and `packages/*/src`):
   - NO `any`. NO `as` casts. NO `!` non-null assertions. Use type guards.
   - Use `if (!x)` for falsy checks. NEVER write `x === null || x === undefined`.

## 🏗 ARCHITECTURE & BOUNDARIES

- `packages/common`: Shared types, constants (Apache-2.0). _No duplicating these in apps._
- `packages/asyncapi`: `asyncapi.yaml` is the **single source of truth** for the optional control-plane WebSocket protocol. Generates Go types into `apps/runwisp/internal/generated/protocol/`. **Never hand-write message types.**
- `packages/{ui, eslint-config, typescript-config}`: Shared Svelte component library and tooling configs.
- `apps/runwisp`: Go standalone cron daemon binary. Single binary with embedded SQLite, REST API, SSE log streaming, and optional outbound control-plane integration (`internal/cloud/`).
  - `cmd/runwisp/`: CLI entry point. Root command boots the TUI; subcommands: `daemon`, `validate`, `status`, `list`, `exec`, `tui`, `cloud`, `openapi`. Also: first-run setup, password handling, port checks, daemon spawn/lifecycle.
  - `cmd/e2e-fake-daemon/`: Test-only binary that impersonates a daemon for cloud E2E tests. Not shipped.
  - `internal/model/`: Core domain types (`Task`, `Run`, enums, concurrency/restart/missed-run policies).
  - `internal/server/`: HTTP server (huma), REST routes, CHAP auth, SSE log streaming.
  - `internal/runtime/`: Task scheduler, run manager (concurrency policies, queuing), catchup, retention, retry.
  - `internal/executor/`: Low-level process execution engine (spawn, stdio capture, signal, exit reaping).
  - `internal/notify/`: Notification subsystem. Subscribes to the event bus, evaluates routing rules against per-event predicates, dispatches to channels (in-app, Slack, Telegram) via per-action workers, retries with backoff, coalesces in-app bursts, and emits a `notify_delivery_failed` synthetic event on permanent failure (in-app only — cycle guard). See `channel/`, `coalesce/`, `kinds/`, `render/`, `configload/`.
  - `internal/cloud/`: Optional outbound control-plane client (see section below).
  - `internal/config/`
  - `internal/storage/`
  - `internal/events/`: In-memory pub/sub event bus for run lifecycle and log-line events.
  - `internal/apiclient/`: HTTP client used by CLI commands and TUI to talk to a running daemon.
  - `internal/tui/`: Bubbletea TUI.
  - `internal/datadir/`: Data directory helpers — PID file, password resolution, JWT secret generation.
  - `internal/fingerprint/`: Deterministic human-readable instance fingerprint (machine-id + cwd).
  - `internal/logutil/`: Log file indexing and metadata helpers.
  - `internal/ui/`: Serves the embedded Svelte dashboard static assets (`dist/` is populated by the build script — do not commit).
  - `internal/version/`: Single `Version` string, overridden at build time via ldflags from `CHANGELOG.md`.
  - `internal/testutil/`
  - `internal/generated/protocol/`
  - `tests/e2e/`
- `apps/ui`: Svelte 5 embedded web dashboard (`@runwisp/web-ui`; built as static assets, served by the daemon). REST in `src/lib/api.ts`, SSE in `src/lib/logs.ts`, rune stores under `src/lib/stores/`, components under `src/lib/components/`.
- `apps/docs`: Astro/Starlight docs site (`apps/docs/src/content/docs/`).
- `packages/assets`: Repo-level binary assets (README screenshots). Not a TS package.

## 💾 DATA MODEL & I/O

- **Embedded SQLite** (`database/sql` + `modernc.org/sqlite`) is the only persistent store. No external DB, no KV, no Redis. Migrations are forward-only; old daemons must tolerate reading rows written by slightly newer ones where feasible.
- **IDs**: Monotonic ULIDs exclusively. No auto-increment integers on user-visible entities, no UUIDv4.
- **Logs**: per-task log files on disk under the data dir, indexed by `internal/logutil/`. SQLite stores run metadata, not log bodies. Rotation/overflow is governed by TOML (`log_max_size`, `log_on_full`).
- **Clock & time**: use injected clock interfaces in `internal/runtime/`. Cron expressions respect the daemon's local TZ unless explicitly scoped (document any TZ change as user-facing).
- **Events**: `internal/events/` is in-memory, best-effort, per-process. It is **not** a durability mechanism — if something must survive restart, it lives in SQLite or on disk.

## 🛰 OPTIONAL CONTROL-PLANE INTEGRATION (`internal/cloud/`)

The daemon can optionally connect outbound to a control-plane peer that speaks the protocol in `packages/asyncapi/asyncapi.yaml`. Rules:

- **Strictly opt-in.** Daemon must boot, schedule, run, and serve UI with the integration disabled, unconfigured, or unreachable. Connection failures are logged, retried with backoff, and never user-visible as errors in the hot path.
- **Protocol only via generated types.** Messages come from `packages/asyncapi/asyncapi.yaml`; `internal/generated/protocol/` is the only consumer-facing surface. Never hand-roll a message.
- **Allowed inbound surface on the daemon:**
  1. **Observability push** — run status, logs, history, health snapshots sent outbound.
  2. **Trigger/stop commands** against tasks defined in `runwisp.toml`.
  3. **Ad-hoc task execution** — the peer may request an ephemeral task run, **only** when explicitly opted-in via TOML (`daemon.allow_cloud_dispatch`). Default is off. Ad-hoc runs never modify the TOML task set — they are one-shot executions, logged like any other run.
- **Backpressure.** If the peer is slow or disconnected, bound the buffer and drop — never block task execution or local event delivery.

## ⚙️ FUNCTION & STATE DESIGN

1. **No global mutable state.** Pass dependencies via constructors / function parameters. Package-level constants and immutable defaults are fine.
2. **Pure where possible.** Validators, mappers, predicates, formatters: standalone pure functions.
3. **Group state with its lifecycle.** Mutable state lives on a struct (Go) or class (TS) that owns it and has a clear start/stop or open/close — connections, caches, schedulers, dispatchers. Other code observes via interfaces, never reaches in.

## 🧪 TESTING PHILOSOPHY

- **Unit tests**: no server, no real SQLite file, no real network, no real clock, no real FS. Use `internal/testutil/` fakes. The notify package keeps its own fakes under `internal/notify/testutil/`.
- **E2E tests**: `apps/runwisp/tests/e2e/` exercises the real binary. Hermetic — isolated data dir and ephemeral ports per test.
- **Bug-first**: a fix isn't done until there's a test that would have caught the bug before the fix.

## 🤖 AGENT EXECUTION RULES

1. **Validation commands** (run from repo root via `make …` or the equivalent `bun run …` alias):
   - `make build` — builds the daemon (Makefile orchestrates web UI build → embed → go build).
   - `make test` — `make test` across the workspace (per-package `go test` / `vitest`). Excludes Playwright; use `make test-e2e` after a real build for browser tests.
   - `make check` — Go vet/lint via `scripts/check-go.sh` plus TS/Svelte checks. Slow lint (svelte-check, eslint) is cached at root `.cache/*.stamp`; `make clean` or branch switches invalidate.
   - `make generate` — regenerates AsyncAPI Go types, common API types, and `apps/runwisp/openapi.json`. Run after editing `packages/asyncapi/asyncapi.yaml` *before* committing — downstream Go types must compile.
    - **Before finalizing Go changes**: `make build && make test`.
    - **Before finalizing TS/Svelte changes**: `make check && make test`.
    - **After finalizing changes (or before wrapping up a session)**: `make ci`. This runs generate → format → check → test → test-e2e, the full CI pipeline.
2. **TOML schema changes require**: docs (`apps/docs/src/content/docs/configuration/`), OpenAPI (`apps/runwisp/openapi.json` via `bun run generate`), `CHANGELOG.md`, and the README config reference if user-visible.
3. **AsyncAPI changes**: edit `packages/asyncapi/asyncapi.yaml` first, then `bun run generate`, then consume the regenerated types in `internal/generated/protocol/`. Never the other way round.
4. **User-facing changes** require a `CHANGELOG.md` entry. The changelog is marketing-facing — tell users what they can do, not how it was implemented. Concise but informative.
5. **Stop and ask** when Prime Directives / Non-Goals / Invariants don't resolve a judgment call. Do not silently pick a direction that might violate the vision.
6. **Pre-1.0, no back-compat hedges.** No deprecation shims, no "tolerate old shape", no migration warnings — reject wrong shapes with errors and move on. (There are no users yet.)
7. **Commits**: Do not add `Co-Authored-By: Claude` trailers. Plain commit messages only.
