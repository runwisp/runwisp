# @runwisp/web-ui

SvelteKit web dashboard for RunWisp. Provides task management, execution monitoring, and real-time log viewing. Built on `@runwisp/ui` shared components.

## Development

From the repository root:

```bash
bun install
bun --cwd apps/ui dev
```

The dev server starts at `http://localhost:5173` and proxies API requests to the daemon.

## Configuration

| Variable       | Description         | Default                 |
| -------------- | ------------------- | ----------------------- |
| `VITE_API_URL` | Daemon API base URL | `http://localhost:9477` |

## Architecture

- **Tailwind v4** via `@tailwindcss/vite`, importing `@runwisp/ui/theme.css` for consistent theming.
- **Live updates** via SSE from `/api/runs/stream` — see `src/lib/stores/run-updates.ts`.
- **Shared components** from `@runwisp/ui` (log console, task lists, layouts).

## License

[Apache-2.0](../../LICENSE)
