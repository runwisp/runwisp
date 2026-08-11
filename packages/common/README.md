# @runwisp/common

Shared types, constants, and utility functions for the RunWisp platform.

## What's Inside

- **Types** — `Task`, `Run`, `EndReason`, `RunStatus`, and other domain types re-exported from the generated OpenAPI spec.
- **Constants** — `RUN_PHASES`, `END_REASONS`, `FAILURE_END_REASONS`, `TRIGGERS`.
- **Helpers** — `displayStatus`, `isService`, `isFailureEndReason`.
- **Logging** — `Logger`, `createLoggerFactory`.
- **IDs** — `generateUlid` (monotonic ULIDs).

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
import { generateUlid, displayStatus, type Task } from "@runwisp/common";
```

## License

[Apache-2.0](LICENSE)
