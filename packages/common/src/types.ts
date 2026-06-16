// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

export type {
  Task,
  Run,
  DaemonInfo,
  SystemStats,
  paths as APIPaths,
  components as APIComponents,
} from "./generated/api.js";

import type { components } from "./generated/api.js";

export type RunSelector = components["schemas"]["RunSelector"];

/** A per-execution parameter an operator may supply at manual trigger time. */
export type TaskParam = components["schemas"]["TaskParam"];

/**
 * EndReason is the union of all reasons a run can end. The single source of
 * truth is the Go `model.EndReason` enum, surfaced via the OpenAPI spec
 * (`components.schemas.EndReason`) and consumed here from the generated
 * `api.ts`. Add new reasons in Go; this re-export keeps the TS surface in
 * lockstep.
 */
export type EndReason = components["schemas"]["EndReason"];

// Runtime constants remain manual until the Go app exposes enum metadata.
export const RUN_PHASES = ["pending", "running", "ended"] as const;
export type RunPhase = (typeof RUN_PHASES)[number];

/**
 * Runtime mirror of the generated `EndReason` union. Needed because the
 * generated TS type is structural-only (no runtime form), but consumers
 * (zod schemas, status filters) need an enumerable list. Drift is guarded
 * on both sides: the `satisfies` clause rejects unknown values, and the
 * `_EndReasonsExhaustive` check below rejects missing ones.
 */
export const END_REASONS = [
  "success",
  "failed",
  "stopped",
  "timeout",
  "crashed",
  "skipped",
  "log_overflow",
  "queue_full",
  "dst_skipped",
  "daemon_stopped",
  "missed",
  "start_failed",
] as const satisfies readonly EndReason[];

// Compile-time exhaustiveness: identity-asserts that every EndReason
// produced by the Go spec appears in END_REASONS. If Go adds a new value
// without updating this array, this line fails with a missing-member error.
type _EndReasonsExhaustive = Exclude<
  EndReason,
  (typeof END_REASONS)[number]
> extends never
  ? true
  : false;
// Triggers a compile-time error if any EndReason is missing from END_REASONS.
true satisfies _EndReasonsExhaustive;

/**
 * End reasons treated as failures by retry policy, dashboards, and the
 * "Last run failed" UI surface. Mirrors `retry.IsFailureReason` in Go —
 * keep them in sync.
 *
 * queue_full / dst_skipped are policy outcomes, not failures, so they
 * intentionally stay out of this list (alongside skipped). daemon_stopped
 * is operator-driven shutdown, not a task fault.
 */
export const FAILURE_END_REASONS = [
  "failed",
  "crashed",
  "timeout",
  "log_overflow",
  "start_failed",
] as const satisfies readonly EndReason[];
export type FailureEndReason = (typeof FAILURE_END_REASONS)[number];

export function isFailureEndReason(
  reason: EndReason | null | undefined,
): reason is FailureEndReason {
  if (!reason) return false;
  for (const candidate of FAILURE_END_REASONS) {
    if (candidate === reason) return true;
  }
  return false;
}

/** Union of phases and end-reasons for UI display/filtering. */
export const RUN_STATUSES = [...RUN_PHASES, ...END_REASONS] as const;

export type RunStatus = (typeof RUN_STATUSES)[number];

export const TRIGGERS = ["cron", "api", "cloud", "service", "startup"] as const;

export type Trigger = (typeof TRIGGERS)[number];

/** Derive a display status from a run's phase + optional end reason. */
export function displayStatus(
  phase: RunPhase,
  endReason?: EndReason | null,
): RunStatus {
  if (phase === "ended") {
    if (endReason) return endReason;
    return "ended";
  }
  return phase;
}

export function isService(kind: string | undefined | null): boolean {
  return kind === "service";
}
