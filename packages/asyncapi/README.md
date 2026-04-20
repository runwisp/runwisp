# @runwisp/asyncapi

AsyncAPI specification and code generation for the RunWisp daemon-to-cloud WebSocket protocol.

## Overview

The [asyncapi.yaml](asyncapi.yaml) file is the **single source of truth** for all WebSocket message types exchanged between the Go daemon and the control plane. Code generation produces:

- **Go types** — Written to `apps/runwisp/internal/generated/protocol/`
- **Zod schemas** (TypeScript) — Written to the control plane (when available)

## Code Generation

```bash
bun --cwd packages/asyncapi generate
```

This runs `scripts/generate.js`, which:

1. Parses `asyncapi.yaml`
2. Generates Go structs via `@asyncapi/modelina-cli`
3. Generates TypeScript Zod schemas (if the control plane is present in the workspace)

## Editing the Protocol

1. Modify `asyncapi.yaml`
2. Run `bun --cwd packages/asyncapi generate`
3. Verify the workspace: `bun run build && bun run test && bun run check`

**Never hand-edit generated files** — always modify the spec and regenerate.

## License

[Apache-2.0](LICENSE)
