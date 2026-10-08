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
    CalendarX,
    OctagonX,
    HeartCrack,
} from "@lucide/svelte";
import type { Component } from "svelte";
import type { RunStatus } from "@runwisp/common";

interface RunStatusConfig {
    icon: Component;
    color: string;
    bg: string;
    dot: string;
    /** `dot` without the running pulse, for static markers (spines, filter swatches). */
    solidDot: string;
    badge: string;
    /**
     * Accent colour for statuses that mean "something to triage", or undefined.
     * A deliberate stop or a skip is not an alarm, and success least of all.
     */
    alarm: string | undefined;
    /** One-sentence explanation of what this status means, for tooltips. */
    description: string;
}

// Full literal class strings (not built from a tone name) so Tailwind's
// scanner picks them up.
const DANGER = {
    color: "text-danger-surface",
    bg: "bg-danger-soft",
    dot: "bg-danger-surface",
    solidDot: "bg-danger-surface",
    badge: "bg-danger-soft text-danger-soft-text",
    alarm: "var(--color-danger-surface)",
};
const WARNING = {
    color: "text-warning-surface",
    bg: "bg-warning-soft",
    dot: "bg-warning-surface",
    solidDot: "bg-warning-surface",
    badge: "bg-warning-soft text-warning-soft-text",
    alarm: "var(--color-warning-surface)",
};
const NEUTRAL = {
    color: "text-on-surface-muted",
    bg: "bg-surface-sunken",
    dot: "bg-on-surface-faint",
    solidDot: "bg-on-surface-faint",
    badge: "bg-surface-sunken text-on-surface",
    alarm: undefined,
};

export const RUN_STATUS_CONFIG: Record<RunStatus, RunStatusConfig> = {
    running: {
        icon: LoaderCircle,
        color: "text-info-surface",
        bg: "bg-info-soft",
        dot: "bg-info-surface animate-pulse",
        solidDot: "bg-info-surface",
        badge: "bg-info-soft text-info-soft-text",
        alarm: undefined,
        description: "This run is executing right now.",
    },
    succeeded: {
        icon: CircleCheck,
        color: "text-success-surface",
        bg: "bg-success-soft",
        dot: "bg-success-surface",
        solidDot: "bg-success-surface",
        badge: "bg-success-soft text-success-soft-text",
        alarm: undefined,
        description: "The run finished with exit code 0 - everything OK.",
    },
    failed: {
        ...DANGER,
        icon: CircleX,
        description: "The run exited with a non-zero code.",
    },
    crashed: {
        ...DANGER,
        icon: CircleAlert,
        description:
            "The process was killed, or the daemon found it still 'running' after a hard crash and marked it crashed. It was not resumed.",
    },
    stopped: {
        ...WARNING,
        alarm: undefined,
        icon: CircleStop,
        description: "A human or an external script manually stopped this run before it finished.",
    },
    timeout: {
        ...WARNING,
        icon: TimerOff,
        description:
            "The run exceeded its configured timeout and was terminated. Timeout duration can be changed.",
    },
    skipped: {
        ...NEUTRAL,
        icon: SkipForward,
        description:
            'Skipped by the concurrency policy (on_overlap = "skip") because a previous run was still going.',
    },
    log_overflow: {
        ...DANGER,
        icon: FileExclamationPoint,
        description: "The run hit log_max_size and was handled because of log_on_full.",
    },
    queue_full: {
        ...WARNING,
        icon: SkipForward,
        description: "Skipped because the task's queue was already at max_queued.",
    },
    dst_skipped: {
        ...NEUTRAL,
        icon: SkipForward,
        description:
            "Skipped: this cron tick was the duplicate half of a DST fall-back, so it was recorded but not run.",
    },
    daemon_stopped: {
        ...WARNING,
        icon: CircleStop,
        description:
            "The daemon shut down while this run was in flight and it exceeded shutdown_timeout; it was not resumed.",
    },
    missed: {
        ...DANGER,
        icon: CalendarX,
        description:
            "A scheduled run never happened because the daemon was down. Detected and recorded on restart.",
    },
    start_failed: {
        ...DANGER,
        icon: OctagonX,
        description:
            "The run (or service instance) kept failing; after restart_attempts consecutive failures, RunWisp gave up restarting it automatically.",
    },
    unhealthy: {
        ...DANGER,
        icon: HeartCrack,
        description:
            "The service instance kept failing its health_check, so RunWisp stopped it. Its restart policy decides what happens next.",
    },
    pending: {
        ...NEUTRAL,
        icon: Clock,
        description: "Queued and waiting to start.",
    },
    ended: {
        ...NEUTRAL,
        color: "text-on-surface-faint",
        icon: CircleDashed,
        description: "The run finished. No specific end reason was recorded.",
    },
};
