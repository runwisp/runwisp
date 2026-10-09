# @runwisp/asyncapi

AsyncAPI specification and code generation for the RunWisp daemon-to-station WebSocket protocol.

## Overview

The [asyncapi.yaml](asyncapi.yaml) file is the **single source of truth** for all WebSocket message types exchanged between the Go daemon and the control plane. Code generation produces:

- **Go types**: `apps/runwisp/internal/generated/protocol/protocol.go`
- **Zod schemas** (TypeScript): `src/generated/protocol-messages.ts`, exported from `src/index.ts`

## Code Generation

```bash
bunx moon run asyncapi:generate
```

This runs `scripts/generate.js`, which parses `asyncapi.yaml`, writes the Zod schemas, and writes the Go types with `scripts/go.js`. Go type mapping comes from the schema itself: `format: int64` → `int64`, `format: date-time` → `time.Time`, `nullable` → pointer, optional → `omitzero`, enums → `type X string` constants. Inline enums are named after their property, so two inline enums on the same property name must move to `components.schemas`.

## Editing the Protocol

1. Modify `asyncapi.yaml`
2. Run `bunx moon run asyncapi:generate`
3. Verify the workspace: `bun run ci`

**Never hand-edit generated files** — always modify the spec and regenerate.

## License

[Apache-2.0](LICENSE)
