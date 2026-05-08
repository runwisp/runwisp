# RunWisp — Agent Directives

**License**: Apache-2.0 · **Status**: Pre-1.0 (breaking changes permitted)
**Stack**: Go 1.25 daemon, Svelte 5 (runes) + Tailwind UI, Bun + Nx monorepo, embedded SQLite (GORM), AsyncAPI-defined optional control-plane protocol.

## 🎯 PRODUCT VISION (read this first — it outranks everything below)

RunWisp replaces **crond + supervisord** with one small Go binary that a single developer can drop on a VPS, a Raspberry Pi, or into a Docker image and immediately see *what ran, when, why it failed, and what it printed*. Every design decision must serve that sentence.

**Who we're for**: the solo dev / small ops team whose current options are "edit crontab over SSH" or "stand up Airflow". We meet them in the middle.

**Prime directives** (in priority order; when they conflict, the higher one wins):

1. **Nothing silently fails.** Every run has an exit code, duration, timestamps, and captured output — persisted, browsable, and streamable. If a change makes failures invisible, reject it.
2. **One binary, zero runtime deps.** No Python, Node, external DB, systemd, or sidecars required to run RunWisp. SQLite and the web UI are *embedded*. Do not add runtime deps; prefer a vendored Go lib over a service.
3. **TOML is the sole source of truth.** `runwisp.toml` defines every task. The REST API and Web UI are **read-only + trigger** — they never mutate task definitions. Schema changes are user-visible breaking changes; treat the TOML surface as an API even pre-1.0. Never add a feature that *requires* the UI or API to configure.
4. **Local-first, offline-complete.** The daemon must work fully offline. Any network integration (`internal/cloud/`) is strictly optional — no feature may degrade when it's disabled or unreachable.
5. **Built for the individual and the small team.** Every core capability ships in the binary: scheduling, supervision, observability, web UI, TUI, REST. No artificial limits, no feature flags gating basics. If it helps one operator run their tasks well, it belongs in the binary.
6. **Boring in prod.** Predictable resource use, graceful shutdown, recoverable state after crash or kill -9. Prefer a simple mechanism that's easy to reason about over a clever one that saves 5%. *(Perf target: ~15 MB RAM idle is aspirational at v0.1.x; it will harden into a CI-enforced budget before 1.0. Don't regress it casually.)*

## 🚫 NON-GOALS

These are **not** bugs to fix or features to add. If a user/issue/PR asks for them in the daemon, push back — they belong in a different tool, or at most in an external integration that speaks to RunWisp over its documented protocol.

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
- **Config reload is restart-only.** A running daemon keeps its in-memory task set for its entire lifetime; to pick up TOML changes, the operator restarts the daemon. No file-watchers, no auto-reload, no SIGHUP handler, no `runwisp reload` command. (Reload-without-restart is on the roadmap; until it lands, do not assume it exists.)
- **Crash safety**: Killing the daemon (SIGKILL, power loss) must not corrupt state. On restart, any run that was in-flight is marked **interrupted** with a terminal status — it is **not resumed**. A fresh execution may then be created by the normal scheduling/catchup logic.
- **Determinism of scheduling**: Given the same TOML + clock, the scheduler produces the same firings. Randomness, wall-clock reads, and FS I/O are injected, never called inline in scheduling logic.
- **No required network**: Daemon startup, task execution, UI serving, and TUI must all work with the NIC unplugged. Any outbound integration attempts happen in the background and never block the hot path.
- **Single writer per task**: Exactly one goroutine/run-manager owns a task's run lifecycle. Any other code observing state does so via `internal/events/` or read-only storage queries.
- **Generated code is write-once**: `internal/generated/protocol/` is regenerated from `packages/asyncapi/asyncapi.yaml`. Never hand-edit. If you need a new message, edit the AsyncAPI spec.
- **Embedded assets stay embedded**: The web UI ships inside the binary. No "download assets at runtime", no CDN fallback.

## 🔐 TRUST MODEL

- The daemon runs **with the privilege of whoever started it**, executing **user-authored shell** from TOML. This is intentional — it's a cron replacement — but it means:
  - TOML is trusted input; the REST API / UI is not.
  - Never execute user-provided strings from HTTP/WS bodies as shell. `run =` comes from disk only.
  - Secrets (JWT secret, passwords) live under the data dir (`internal/datadir/`) with restrictive perms; never log them; never transmit them over any outbound integration.
- CHAP (challenge-response) auth is the login boundary. JWT is the session. Don't bypass either for "convenience" endpoints.

## 🧠 DECISION HEURISTICS (use when the spec is silent)

When a design question isn't answered by the above, resolve it in this order:

1. **Does it make a failure more visible?** → Yes = lean toward it.
2. **Does it add a runtime dependency or a required network call?** → Yes = reject or make it strictly optional.
3. **Does it change the TOML schema?** → Treat as breaking; document in CHANGELOG, update `runwisp.example.toml`, update OpenAPI.
4. **Does it add state that must survive restart?** → It goes through `internal/storage/` (GORM + SQLite), gets a ULID, and has a reconciliation path on boot.
5. **Does it touch the control-plane protocol?** → Edit `packages/asyncapi/asyncapi.yaml` first, regenerate, then consume the generated types. Never the other way round.
6. **Can a solo dev understand it by reading `runwisp.toml` + the web UI?** → If no, simplify or document.

## 🚨 TECHNICAL DEBT & HYGIENE (HIGHEST PRIORITY)

1. **Boy Scout Rule**: When modifying a file, you MUST fix existing violations (broken naming, dead code, leaked concerns, missing types). **Never increase technical debt.**
2. **Aggressive Extraction**: Extract duplicated logic across 2+ files immediately. Place in the most specific shared location or `packages/common`.
3. **No Reinvention**: Before writing utilities (slug, cron parse, retry, ID gen), use existing `packages/common` functions or install an npm/Go package with >1k GitHub stars.
4. **Code Quality**:
   - NO `any` types. NO `as` casts. NO `!` non-null assertions. Use type guards.
   - Use `if (!x)` for falsy checks. NEVER write `x === null || x === undefined`.
   - NO redundant comments. Comment _why_, never _what_. Code must be self-documenting.

## 🏗 ARCHITECTURE & BOUNDARIES

**Code Location Rules:**

- `packages/common`: Shared types, constants (Apache-2.0). _No duplicating these in apps._
- `packages/asyncapi`: `asyncapi.yaml` is the **single source of truth** for the optional control-plane WebSocket protocol. Generates Go types into `apps/runwisp/internal/generated/protocol/`. **Never hand-write message types.**
- `packages/ui`: Svelte 5 UI component library & layouts (shared between `web-ui` and other frontends).
- `packages/eslint-config` / `packages/typescript-config`: Shared tooling configs.
- `apps/runwisp`: Go standalone cron daemon binary. Single binary with embedded SQLite, REST API, SSE log streaming, and optional outbound control-plane integration (`internal/cloud/`).
  - `cmd/runwisp/`: CLI entry point — daemon lifecycle, run/trigger/status/list/tui commands.
  - `internal/model/`: Core domain types (`Task`, `Run`, enums, concurrency/restart/missed-run policies).
  - `internal/server/`: HTTP server, REST routes, CHAP auth, SSE log streaming.
  - `internal/runtime/`: Task scheduler, run manager (concurrency policies, queuing), catchup, retention, retry.
  - `internal/executor/`: Low-level process execution engine.
  - `internal/cloud/`: Optional outbound control-plane client (WebSocket protocol defined in `packages/asyncapi`, connection lifecycle, ad-hoc dispatch handling).
  - `internal/config/`: TOML config parsing.
  - `internal/storage/`: SQLite persistence (GORM).
  - `internal/events/`: In-memory pub/sub event bus for run lifecycle and log-line events.
  - `internal/apiclient/`: HTTP client used by CLI commands and TUI to talk to a running daemon.
  - `internal/tui/`: Bubbletea terminal UI (home view, exec list, log pane, dialogs).
  - `internal/datadir/`: Data directory helpers — PID file, password resolution, JWT secret generation.
  - `internal/fingerprint/`: Deterministic human-readable instance fingerprint (machine-id + cwd).
  - `internal/logutil/`: Log file indexing and metadata helpers.
  - `internal/ui/`: Serves the embedded Svelte dashboard static assets.
  - `internal/testutil/`: Shared test helpers (in-memory fakes, fixtures).
  - `internal/generated/protocol/`: Auto-generated from `packages/asyncapi`. **Do not edit manually.**
- `apps/ui`: Svelte 5 embedded web dashboard (`@runwisp/web-ui`; built as static assets, served by the daemon).
  - `src/lib/components/`: UI components (auth modal, dashboard views, error panel).
  - `src/lib/stores/`: Svelte rune stores for auth and server data.
  - `src/lib/adapters/`: Browser-side SSE/API adapter.
  - `src/lib/layouts/`: Shared page layout components.
  - `src/lib/types/`: TypeScript type definitions.
  - `src/lib/utils/`: Utility functions (async-data, SSE, log session, sorting, env, task ID).
  - `src/lib/config/`: Shared constants.

**Single Responsibility**: A file must do one thing. If it defines a schema AND a handler, split it.

## 💾 DATA MODEL & I/O

- **Embedded SQLite (GORM)** is the only persistent store. No external DB, no KV, no Redis. Migrations are forward-only; old daemons must tolerate reading rows written by slightly newer ones where feasible.
- **IDs**: Monotonic ULIDs exclusively. No auto-increment integers on user-visible entities, no UUIDv4.
- **Logs**: per-task log files on disk under the data dir, indexed by `internal/logutil/`. SQLite stores run metadata, not log bodies. Rotation/overflow is governed by TOML (`log_max_size`, `log_on_full`).
- **Clock & time**: use injected clock interfaces in `internal/runtime/`. Cron expressions respect the daemon's local TZ unless explicitly scoped (document any TZ change as user-facing).
- **Events**: `internal/events/` is in-memory, best-effort, per-process. It is **not** a durability mechanism — if something must survive restart, it lives in SQLite or on disk.

## 🛰 OPTIONAL CONTROL-PLANE INTEGRATION (`internal/cloud/`)

The daemon can optionally connect outbound to a control-plane peer that speaks the protocol in `packages/asyncapi/asyncapi.yaml`. This is a generic mechanism — any service that implements the protocol can play that role. The daemon's rules for it:

- **Strictly opt-in.** Daemon must boot, schedule, run, and serve UI with the integration disabled, unconfigured, or unreachable. Connection failures are logged, retried with backoff, and never user-visible as errors in the hot path.
- **Protocol only via generated types.** Messages come from `packages/asyncapi/asyncapi.yaml`; `internal/generated/protocol/` is the only consumer-facing surface. Never hand-roll a message.
- **Allowed inbound surface on the daemon:**
  1. **Observability push** — run status, logs, history, health snapshots sent outbound.
  2. **Trigger/stop commands** against tasks defined in `runwisp.toml`.
  3. **Ad-hoc task execution** — the peer may request an ephemeral task run, **only** when explicitly opted-in via TOML (`daemon.allow_cloud_dispatch`). Default is off. Ad-hoc runs never modify the TOML task set — they are one-shot executions, logged like any other run.
- **TOML remains canonical.** The integration never edits, replaces, or shadows the configured task set.
- **Backpressure.** If the peer is slow or disconnected, bound the buffer and drop — never block task execution or local event delivery.

## ⚙️ FUNCTION, CLASS & STATE DESIGN

1. **No Global State**: No file-level mutable state (`let cache = ...`). Pass dependencies via parameters/constructors.
2. **Pure Functions**: Use standalone pure functions for small ops (validators, mappers).
3. **Classes**:
   - Use for grouping related ops with shared context/dependencies (even if stateless) to define clear boundaries.
   - Use for **mutable state**, but only if the state has a clear lifecycle (connections, caches). The class must own the state.
4. **Testability**: Every new service function/method must be testable WITHOUT a server, real DB, or network. Accept interfaces, not concretes.
5. **Side Effects**: I/O, time, and randomness must be injected/behind an interface, not called directly in business logic. (If a function is hard to test, split it).

## 🧪 TESTING PHILOSOPHY

- Unit tests must run without a server, real SQLite file, real network, real clock, or real filesystem. Use `internal/testutil/` fakes. If a new component can't be tested that way, split it until it can.
- Integration tests that need the real binary go under `apps/runwisp/tests/` and must be hermetic (isolated data dir, ephemeral ports).
- A feature isn't done until there's a test that would have caught the bug *before* the fix.

## 🤖 AGENT EXECUTION RULES

1. Use the built-in file tools (`view`, `grep`, `glob`, `edit`) — not shell `cat`/`find`/`grep`.
2. Before finalizing **Go** changes: `bun run build && bun run test` (and `bun run check` if lint/TS is adjacent).
3. Before finalizing **TypeScript/Svelte** changes: `bun run check && bun run test`.
4. Changes to `packages/asyncapi/asyncapi.yaml` **require regeneration** before commit; downstream Go types must compile.
5. Changes to the TOML config schema **require** updating: `runwisp.example.toml`, OpenAPI (`apps/runwisp/openapi.json`), CHANGELOG, and the README config reference if behavior is user-visible.
6. When a judgment call arises that Prime Directives / Non-Goals / Invariants don't clearly resolve: **stop and ask the user**. Do not silently pick a direction that might violate vision.
7. If you introduced a user-facing change, add or modify CHANGELOG.md. Changelog is "marketing-facing", it's made to tell users what they can expect from the new version, not how devs can configure the new feature. Try to be concise but informative about it.
