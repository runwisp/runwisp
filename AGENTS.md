# RunWisp - Agent Directives

**Context**: Open-source cron replacement and process supervisor owned by **PoppyCake, s.r.o.** Go daemon (Apache-2.0) with an embedded Svelte web dashboard, optional cloud connectivity. Pre-1.0 (breaking changes permitted).
**Stack**: Bun, Nx, Go, Svelte 5 runes, Tailwind CSS.

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
- `packages/asyncapi`: `asyncapi.yaml` is the **single source of truth** for the cloud WS protocol. Generates Go types into `apps/runwisp/internal/generated/protocol/`. **Never hand-write message types.**
- `packages/ui`: Svelte 5 UI component library & layouts (shared between `web-ui` and other frontends).
- `packages/eslint-config` / `packages/typescript-config`: Shared tooling configs.
- `apps/runwisp`: Go standalone cron daemon binary. Single binary with embedded SQLite, REST API, SSE log streaming, and optional cloud connectivity (`internal/cloud/`).
  - `cmd/runwisp/`: CLI entry point — daemon lifecycle, run/trigger/status/list/tui commands.
  - `internal/model/`: Core domain types (`Task`, `Run`, enums, concurrency/restart/missed-run policies).
  - `internal/server/`: HTTP server, REST routes, CHAP auth, SSE log streaming.
  - `internal/runtime/`: Task scheduler, run manager (concurrency policies, queuing), catchup, retention, retry.
  - `internal/executor/`: Low-level process execution engine.
  - `internal/cloud/`: Cloud platform client (WebSocket protocol, lifecycle, dispatch).
  - `internal/config/`: YAML config parsing.
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

- **Embedded SQLite**: The daemon uses embedded SQLite for persistence (runs, logs, state).
- **IDs**: Use Monotonic ULIDs exclusively.

## ⚙️ FUNCTION, CLASS & STATE DESIGN

1. **No Global State**: No file-level mutable state (`let cache = ...`). Pass dependencies via parameters/constructors.
2. **Pure Functions**: Use standalone pure functions for small ops (validators, mappers).
3. **Classes**:
   - Use for grouping related ops with shared context/dependencies (even if stateless) to define clear boundaries.
   - Use for **mutable state**, but only if the state has a clear lifecycle (connections, caches). The class must own the state.
4. **Testability**: Every new service function/method must be testable WITHOUT a server, real DB, or network. Accept interfaces, not concretes.
5. **Side Effects**: I/O, time, and randomness must be injected/behind an interface, not called directly in business logic. (If a function is hard to test, split it).

## 🤖 AGENT EXECUTION RULES

1. Always use the built-in file tools (`read` for reading files, `grep` for searching content, `glob` for searching file paths) instead of standard shell commands like `cat`, `find`, or `grep`.
2. For Go changes in `apps/runwisp`, always verify with `bun run build && bun run test` before committing.
3. For TypeScript changes, run `bun run check` and `bun run test`.
