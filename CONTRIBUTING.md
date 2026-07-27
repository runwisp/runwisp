# Contributing to RunWisp

Thanks for taking the time to look at the code. The daemon and web UI (`apps/`) are GPL-3.0-or-later; the shared libraries under `packages/` are Apache-2.0. There is no CLA — your contribution stays yours, licensed under the terms of whichever tree it lands in.

> RunWisp is **pre-1.0**. Breaking changes are permitted; back-compat shims are not. Read [AGENTS.md](AGENTS.md) for the project's design principles, prime directives, and non-goals before proposing significant changes.

## Prerequisites

- **Go 1.25+** — the daemon
- **[Bun](https://bun.sh/) 1.3+** — workspace manager, TS/Svelte builds, codegen

That's it. No Docker, no Postgres, no Redis required. The repo bootstraps with one `bun install`.

## Setup

```bash
git clone https://github.com/runwisp/runwisp
cd runwisp

bun install                # install JS/TS workspace deps
bun run build              # builds the web UI, then the daemon
./apps/runwisp/runwisp     # smoke-test the binary
```

## Repository layout

| Path                                   | What lives there                                                          |
| -------------------------------------- | ------------------------------------------------------------------------- |
| `apps/runwisp/`                        | Go daemon — scheduler, supervisor, REST API, TUI, embedded UI server      |
| `apps/ui/`                             | Svelte 5 web dashboard (embedded into the daemon at build time)           |
| `apps/docs/`                           | [docs.runwisp.com](https://docs.runwisp.com) — Astro / Starlight          |
| `packages/asyncapi/`                   | AsyncAPI spec for the optional control-plane protocol — codegen source    |
| `packages/common/`                     | Shared TypeScript types and constants                                     |
| `packages/ui/`                         | Shared Svelte component library                                           |
| `packages/{eslint,typescript}-config/` | Shared tooling configs                                                    |
| `packages/assets/`                     | Repo binary assets (README screenshots)                                   |

Daemon internals live under `apps/runwisp/internal/`:
`runtime/` (scheduler + run manager), `executor/` (process spawning, stdio capture),
`server/` (REST, CHAP auth, JWT, SSE), `storage/` (`database/sql` + embedded SQLite),
`notify/` (notification routing), `cloud/` (optional outbound control plane),
`tui/` (Bubbletea TUI), `events/` (in-memory pub/sub bus).

## Development workflow

Branch off `main`. One PR per logical change.

Before pushing, run the full pipeline from the repo root:

```bash
bun run ci             # generate + format + check + test + test-e2e
bun run build          # Go binary build — required for daemon changes
```

The build system has three layers, each with one job:

1. **bun** installs dependencies and provides the root `package.json` scripts — thin aliases onto moon targets, nothing more. There are no per-package `package.json` scripts.
2. **[moon](https://moonrepo.dev/)** (a dev dependency; `bunx moon ...`) owns the task graph: every task, its inputs, outputs, and dependencies lives in the `moon.yml` next to the code it builds. Tasks invoke tools directly (`vite`, `eslint`, `go test`, ...).
3. **Shell scripts** (`scripts/`, `apps/runwisp/scripts/`) implement anything bigger than a one-liner.

`ci` chains three moon invocations — `generate` (sqlc, AsyncAPI Go types, `openapi.json`, common API types), then `format`, then `check` + `test` + `test-e2e` (playwright against the built binary) in parallel — chained because `format` mutates files the later stages read. When iterating, run a single target with `bunx moon run <project>:<task>` (list them with `bunx moon query tasks`). Moon caches every task by its input hashes under `.moon/cache/`; `bun run clean` resets everything.

Iterating on the UI:

- `bun run dev` — dev build + launches the daemon
- `bun run web-ui` — Svelte dev server against an already-running daemon
- `bun run theme` — the shared component library playground
- `bun run screenshots` — regenerates the docs Web UI **and** TUI screenshots (`apps/docs/src/assets/screenshots/`) from a demo-seeded daemon. On-demand only, not part of `ci`; the TUI shots are captured by driving the real `runwisp tui` in a pty and replaying the stream through xterm.js.

## Making changes

### TOML schema (`runwisp.toml`)

User-visible. Requires **all** of:

1. Schema + validator update under `apps/runwisp/internal/config/`.
2. `bun run ci` (refreshes `apps/runwisp/openapi.json` as part of `generate`).
3. Docs update in `apps/docs/src/content/docs/configuration/`.
4. A [CHANGELOG.md](CHANGELOG.md) entry under the unreleased section.
5. README config reference update if user-visible.

### Control-plane protocol (`packages/asyncapi/asyncapi.yaml`)

The AsyncAPI YAML is the **single source of truth**. Workflow:

1. Edit `packages/asyncapi/asyncapi.yaml`.
2. Run `bun run generate` (or `ci`) — regenerates Go types into `apps/runwisp/internal/generated/protocol/`.
3. Implement the new messages on the consumer side (`apps/runwisp/internal/cloud/`).

Never hand-edit anything under `internal/generated/protocol/` — it's regenerated.

### REST API (`apps/runwisp/internal/server/`)

Routes are registered with [huma](https://huma.rocks/). `bun run generate` refreshes `apps/runwisp/openapi.json` from the registered routes. Auth-touching changes need a smoke test against a real daemon — exercise both the local Unix-socket path (CLI/TUI) and the password + session-cookie path (Web UI / remote REST), not just unit tests.

### Web UI (`apps/ui/`)

The dashboard is embedded into the binary at build time. REST in `src/lib/api.ts`, SSE in `src/lib/logs.ts`, rune-based stores under `src/lib/stores/`, components under `src/lib/components/`.

## Code style

### Go

- `gofmt` and `go vet` clean — `bun run check` enforces both.
- Inject clocks, randomness, and FS dependencies into the scheduler. Never call `time.Now()` inline in scheduling logic.
- Group mutable state with its lifecycle (a struct with start/stop). Avoid package-level mutable state.
- IDs on user-visible entities are monotonic ULIDs — no auto-increment integers, no UUIDv4.

### TypeScript / Svelte

- No `any`. No `as` casts. No `!` non-null assertions. Use type guards.
- Use `if (!x)` for falsy checks. Never write `x === null || x === undefined`.
- Svelte 5 runes only — no legacy reactive `$:` syntax.

### General

- Comments explain **why**, not **what**. Default to no comment.
- Keep functions small and pure where possible.
- Fix adjacent violations in files you touch — the Boy Scout Rule.

## Testing

- **Unit tests** must not touch real time, real network, real SQLite files, or real filesystems. Use the fakes in `apps/runwisp/internal/testutil/` (and the notify-specific fakes in `apps/runwisp/internal/notify/testutil/`).
- **End-to-end tests** in `apps/runwisp/tests/e2e/` exercise the real binary. They're hermetic — isolated data dir and ephemeral ports per test.
- **Bug fixes ship with a test** that would have caught the bug before the fix. No exceptions.

## Commits and pull requests

- Write commit messages that explain **why** the change is needed. The reviewer reads `git log`, not your inner monologue.
- Plain commit messages — no `Co-Authored-By: Claude` (or other tool) trailers.
- Pre-1.0 means no deprecation shims, no "tolerate the old shape" branches, no migration warnings. Reject wrong shapes with errors and move on.
- PR description should call out anything user-visible (TOML schema, REST API, CLI flags) so it lands correctly in [CHANGELOG.md](CHANGELOG.md).

## Reporting bugs

- [GitHub Issues](https://github.com/runwisp/runwisp/issues) for bugs and feature requests.
- For security vulnerabilities, see [SECURITY.md](SECURITY.md) — **do not file a public issue**.

## License

Contributions to `apps/` are licensed GPL-3.0-or-later; contributions to `packages/` are licensed Apache-2.0. By submitting a pull request you agree your contribution is licensed under the terms of the tree it lands in. You keep your copyright; we don't require a CLA.
