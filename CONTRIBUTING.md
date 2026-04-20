# Contributing to RunWisp

## Getting Started

### Prerequisites

- **Go 1.25+** (for the binary)
- **[Bun](https://bun.sh/) 1.3+** (for TypeScript packages and UI)
- **Docker** (optional, for containerized binary builds)

### Setup

```bash
git clone https://github.com/runwisp/runwisp
cd runwisp

# Install TypeScript dependencies
bun install

# Build everything
bun run build
```

### Project Areas

| Area        | Path               | Language   |
| ----------- | ------------------ | ---------- |
| Go binary   | `apps/runwisp/`    | Go         |
| Web UI      | `apps/ui/`         | Svelte/TS  |
| Design      | `packages/ui/`     | Svelte/TS  |
| Common code | `packages/common/` | TypeScript |

## Development Workflow

1. **Fork** the repository and create a feature branch from `main`.
2. **Make your changes** with clear, focused commits.
3. **Test your changes**:
   - Build: `bun run build`
   - Test: `bun run test`
   - Check and lint: `bun run check`
4. **Submit a Pull Request** with a clear description of what changed and why.

## Code Style

### TypeScript

- No `any` types, `as` casts, or `!` non-null assertions — use type guards.
- Use `if (!x)` for falsy checks.
- Run `bun run check` before committing.

### Go

- Follow standard Go conventions (`gofmt`, `go vet`).
- Verify Go changes with `bun run build`, `bun run test`, and `bun run check`.

### General

- Comment _why_, not _what_. Code should be self-documenting.
- Keep functions small and testable.
- Avoid global mutable state.

## Architecture Guidelines

- **`packages/common`**: Shared types and utilities (Apache-2.0). Don't duplicate these in apps.
- **`packages/asyncapi`**: Single source of truth for WebSocket protocol schemas.
- **`apps/runwisp`**: Standalone Go binary. All daemon logic lives here.

## Reporting Issues

- Use [GitHub Issues](https://github.com/runwisp/runwisp/issues) for bugs and feature requests.
- For security vulnerabilities, see [SECURITY.md](SECURITY.md).

## License

RunWisp is licensed under [Apache-2.0](LICENSE). By submitting a pull request, you agree that your contribution is licensed under the same terms. You keep your copyright — we don't require a CLA.
