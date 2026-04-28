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
] as const;
export type EndReason = (typeof END_REASONS)[number];

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

export function isLongRunningTask(
  kind: string | undefined | null,
  restartPolicy: string | undefined | null,
  concurrencyLimit: number | undefined | null,
): boolean {
  if (kind === "service") return true;
  return restartPolicy === "always" && (concurrencyLimit ?? 1) <= 1;
}

export function isService(kind: string | undefined | null): boolean {
  return kind === "service";
}
