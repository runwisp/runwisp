// Copyright (c) PoppyCake, s.r.o. SPDX-License-Identifier: Apache-2.0

import {
    CircleCheck,
    CircleX,
    CircleAlert,
    LoaderCircle,
    Clock,
    CircleStop,
    TimerOff,
    CircleDashed,
    SkipForward,
    FileExclamationPoint,
} from "@lucide/svelte";
import type { Component } from "svelte";
import { displayStatus, type RunStatus, type Run } from "@runwisp/common";

export interface RunStatusConfig {
    icon: Component;
    color: string;
    bg: string;
    border: string;
    dot: string;
    badge: string;
    /** One-sentence explanation of what this status means, for tooltips. */
    description: string;
}

export const RUN_STATUS_CONFIG: Record<RunStatus, RunStatusConfig> = {
    running: {
        icon: LoaderCircle,
        color: "text-info-surface",
        bg: "bg-info-soft",
        border: "border-info-soft",
        dot: "bg-info-surface animate-pulse",
        badge: "bg-info-soft text-info-soft-text",
        description: "This run is executing right now.",
    },
    success: {
        icon: CircleCheck,
        color: "text-success-surface",
        bg: "bg-success-soft",
        border: "border-success-soft",
        dot: "bg-success-surface",
        badge: "bg-success-soft text-success-soft-text",
        description: "The run finished with exit code 0.",
    },
    failed: {
        icon: CircleX,
        color: "text-danger-surface",
        bg: "bg-danger-soft",
        border: "border-danger-soft",
        dot: "bg-danger-surface",
        badge: "bg-danger-soft text-danger-soft-text",
        description: "The run exited with a non-zero code.",
    },
    crashed: {
        icon: CircleAlert,
        color: "text-danger-surface",
        bg: "bg-danger-soft",
        border: "border-danger-soft",
        dot: "bg-danger-surface",
        badge: "bg-danger-soft text-danger-soft-text",
        description:
            "The process was killed, or the daemon found it still 'running' after a hard crash and marked it crashed (exit -2). It was not resumed.",
    },
    stopped: {
        icon: CircleStop,
        color: "text-warning-surface",
        bg: "bg-warning-soft",
        border: "border-warning-soft",
        dot: "bg-warning-surface",
        badge: "bg-warning-soft text-warning-soft-text",
        description: "An operator stopped this run before it finished.",
    },
    timeout: {
        icon: TimerOff,
        color: "text-warning-surface",
        bg: "bg-warning-soft",
        border: "border-warning-soft",
        dot: "bg-warning-surface",
        badge: "bg-warning-soft text-warning-soft-text",
        description: "The run exceeded its configured timeout and was terminated.",
    },
    skipped: {
        icon: SkipForward,
        color: "text-on-surface-muted",
        bg: "bg-surface-sunken",
        border: "border-outline-faint",
        dot: "bg-on-surface-faint",
        badge: "bg-surface-sunken text-on-surface",
        description:
            'Skipped by the concurrency policy (on_overlap = "skip") because a previous run was still going.',
    },
    log_overflow: {
        icon: FileExclamationPoint,
        color: "text-danger-surface",
        bg: "bg-danger-soft",
        border: "border-danger-soft",
        dot: "bg-danger-surface",
        badge: "bg-danger-soft text-danger-soft-text",
        description: "The run hit log_max_size and was handled per log_on_full.",
    },
    queue_full: {
        icon: SkipForward,
        color: "text-warning-surface",
        bg: "bg-warning-soft",
        border: "border-warning-soft",
        dot: "bg-warning-surface",
        badge: "bg-warning-soft text-warning-soft-text",
        description: "Skipped because the task's queue was already at queue_max.",
    },
    dst_skipped: {
        icon: SkipForward,
        color: "text-on-surface-muted",
        bg: "bg-surface-sunken",
        border: "border-outline-faint",
        dot: "bg-on-surface-faint",
        badge: "bg-surface-sunken text-on-surface",
        description:
            "Skipped: this cron tick was the duplicate half of a DST fall-back, so it was recorded but not run.",
    },
    daemon_stopped: {
        icon: CircleStop,
        color: "text-warning-surface",
        bg: "bg-warning-soft",
        border: "border-warning-soft",
        dot: "bg-warning-surface",
        badge: "bg-warning-soft text-warning-soft-text",
        description:
            "The daemon shut down while this run was in flight and it exceeded shutdown_timeout; it was not resumed.",
    },
    pending: {
        icon: Clock,
        color: "text-on-surface-muted",
        bg: "bg-surface-sunken",
        border: "border-outline",
        dot: "bg-on-surface-faint",
        badge: "bg-surface-sunken text-on-surface",
        description: "Queued and waiting to start.",
    },
    ended: {
        icon: CircleDashed,
        color: "text-on-surface-faint",
        bg: "bg-surface-sunken",
        border: "border-outline-faint",
        dot: "bg-on-surface-faint",
        badge: "bg-surface-sunken text-on-surface",
        description: "The run finished. No specific end reason was recorded.",
    },
};

export function getRunStatusConfig(status: RunStatus): RunStatusConfig {
    return RUN_STATUS_CONFIG[status];
}

export function runDisplayStatus(run: Pick<Run, "status" | "end_reason">): RunStatus {
    return displayStatus(run.status, run.end_reason);
}
