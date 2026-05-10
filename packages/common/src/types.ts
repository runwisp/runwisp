// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// Re-export generated API types (single source of truth from Go OpenAPI spec)
export type {
  Task,
  Run,
  DaemonInfo,
  SystemStats,
  paths as APIPaths,
} from "./generated/api.js";

// Runtime constants remain manual until the Go app exposes enum metadata.
export const RUN_PHASES = ["pending", "running", "ended"] as const;
export type RunPhase = (typeof RUN_PHASES)[number];

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
] as const;
export type EndReason = (typeof END_REASONS)[number];

/**
 * End reasons treated as failures by retry policy, dashboards, and the
 * "Last run failed" UI surface. Mirrors `retry.IsFailureReason` in the Go
 * runtime — keep them in sync. queue_full / dst_skipped are policy outcomes,
 * not failures, so they intentionally stay out of this list (alongside
 * skipped). daemon_stopped is operator-driven shutdown, not a task fault.
 */
export const FAILURE_END_REASONS = [
  "failed",
  "crashed",
  "timeout",
  "log_overflow",
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

export const TRIGGERS = ["cron", "api", "cloud"] as const;

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
