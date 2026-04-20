# @runwisp/common

Shared types, constants, and utility functions for the RunWisp platform.

## What's Inside

- **Execution status** — Canonical status types for task executions (`pending`, `running`, `success`, `failed`, etc.)
- **Daemon identity** — Types and helpers for daemon identification and fingerprinting.
- **Roles** — Organization role definitions.
- **Utilities** — ID generation (monotonic ULIDs), slug helpers, and other shared helpers.

## Usage

This package is consumed as a workspace dependency:

```json
{
  "dependencies": {
    "@runwisp/common": "workspace:*"
  }
}
```

```ts
import { CloudExecutionStatus } from "@runwisp/common";
```

## License

[Apache-2.0](LICENSE)
