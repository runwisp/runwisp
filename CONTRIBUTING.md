# Contributing to RunWisp

Thanks for looking at the code. The daemon and web UI (`apps/`) are GPL-3.0-or-later; the shared libraries under `packages/` are Apache-2.0. There's no CLA — your contribution stays yours, licensed under whichever tree it lands in.

RunWisp follows semver. Breaking changes require a major version bump, and back-compat shims within a major line are not used. Read [AGENTS.md](AGENTS.md) for the design principles, prime directives, and non-goals before proposing anything significant. It's also the map of the repository layout if you need one.

## Prerequisites

- Go 1.25+ for the daemon
- [Bun](https://bun.sh/) 1.3+ for the workspace, TS/Svelte builds, and codegen

No Docker, Postgres, or Redis. The repo bootstraps with one `bun install`.

## Setup

```bash
git clone https://github.com/runwisp/runwisp
cd runwisp

bun install                # install JS/TS workspace deps
bun run build              # builds the web UI, then the daemon
./apps/runwisp/runwisp     # smoke-test the binary
```

## Development workflow

Branch off `main`, one PR per logical change. Before pushing, run the full pipeline from the repo root:

```bash
bun run ci                 # generate + format + check + test + test-e2e
```

`ci` is the single command that has to pass. It runs codegen (sqlc, AsyncAPI Go types, `openapi.json`, common API types), then formatting, then check + test + e2e. To iterate on one stage, run its target directly with `moon run <project>:<task>` (`moon query tasks` lists them); moon caches by input hash, so re-runs are cheap. `bun run clean` resets the cache.

Install the moon CLI with `curl -fsSL https://moonrepo.dev/install/moon.sh | bash` (or `proto install moon`).

Iterating on the UI:

- `bun run dev` — dev build plus the daemon
- `bun run web-ui` — Svelte dev server against an already-running daemon
- `bun run theme` — the shared component library playground
- `bun run screenshots` — regenerates the docs Web UI and TUI screenshots from a demo-seeded daemon (on-demand, not part of `ci`)

## Making changes

### TOML schema (`runwisp.toml`)

User-visible, so it needs all of:

1. Schema + validator update under `apps/runwisp/internal/config/`.
2. `bun run ci` (refreshes `apps/runwisp/openapi.json` as part of codegen).
3. Docs update in `apps/docs/src/content/docs/configuration/`.
4. A [CHANGELOG.md](CHANGELOG.md) entry under the unreleased section.
5. README config reference update if user-visible.

### Control-plane protocol (`packages/asyncapi/asyncapi.yaml`)

The AsyncAPI YAML is the single source of truth:

1. Edit `packages/asyncapi/asyncapi.yaml`.
2. Run `bun run generate` (or `ci`) to regenerate Go types into `apps/runwisp/internal/generated/protocol/`.
3. Implement the new messages on the consumer side (`apps/runwisp/internal/cloud/`).

Never hand-edit anything under `internal/generated/protocol/` — it's regenerated.

### REST API (`apps/runwisp/internal/server/`)

Routes are registered with [huma](https://huma.rocks/); `bun run generate` refreshes `apps/runwisp/openapi.json` from them. Auth-touching changes need a smoke test against a real daemon — exercise both the local Unix-socket path (CLI/TUI) and the password + session-cookie path (Web UI / remote REST), not just unit tests.

## Code style

### Go

- `gofmt` and `go vet` clean — `bun run check` enforces both.
- Inject clocks, randomness, and FS dependencies into the scheduler. Never call `time.Now()` inline in scheduling logic.
- Group mutable state with its lifecycle (a struct with start/stop). Avoid package-level mutable state.
- IDs on user-visible entities are monotonic ULIDs — no auto-increment integers, no UUIDv4.

### TypeScript / Svelte

- No `any`, no `as` casts, no `!` non-null assertions. Use type guards.
- Use `if (!x)` for falsy checks. Never write `x === null || x === undefined`.
- Svelte 5 runes only — no legacy `$:` syntax.

### General

- Comments explain why, not what. Default to no comment.
- Keep functions small and pure where you can.
- Fix adjacent violations in files you touch — the Boy Scout Rule.

## Testing

- Unit tests must not touch real time, network, SQLite files, or filesystems. Use the fakes in `apps/runwisp/internal/testutil/` (and the notify-specific ones in `apps/runwisp/internal/notify/testutil/`).
- End-to-end tests in `apps/runwisp/tests/e2e/` exercise the real binary. They're hermetic — isolated data dir and ephemeral ports per test.
- Bug fixes ship with a test that would have caught the bug. No exceptions.

## Commits and pull requests

- Write commit messages that explain why the change is needed. The reviewer reads `git log`, not your inner monologue.
- Plain commit messages — no `Co-Authored-By` (or other tool) trailers.
- No deprecation shims or "tolerate the old shape" branches. Reject wrong shapes with errors; land breaking changes on a major version bump.
- Call out anything user-visible (TOML schema, REST API, CLI flags) in the PR description so it lands correctly in [CHANGELOG.md](CHANGELOG.md).

## Reporting bugs

- [GitHub Issues](https://github.com/runwisp/runwisp/issues) for bugs and feature requests.
- For security vulnerabilities, see [SECURITY.md](SECURITY.md) — do not file a public issue.

## License

Contributions to `apps/` are GPL-3.0-or-later; contributions to `packages/` are Apache-2.0. By submitting a pull request you agree your contribution is licensed under the terms of the tree it lands in. You keep your copyright; there's no CLA.
