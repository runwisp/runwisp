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
} from "@lucide/svelte";
import type { Component } from "svelte";
import { displayStatus, type RunStatus } from "@runwisp/common";
import type { Run } from "@runwisp/common";

export interface RunStatusConfig {
    icon: Component;
    color: string;
    bg: string;
    border: string;
    dot: string;
    badge: string;
}

export const RUN_STATUS_CONFIG: Record<RunStatus, RunStatusConfig> = {
    running: {
        icon: LoaderCircle,
        color: "text-info-surface",
        bg: "bg-info-soft",
        border: "border-info-soft",
        dot: "bg-info-surface animate-pulse",
        badge: "bg-info-soft text-info-soft-text",
    },
    success: {
        icon: CircleCheck,
        color: "text-success-surface",
        bg: "bg-success-soft",
        border: "border-success-soft",
        dot: "bg-success-surface",
        badge: "bg-success-soft text-success-soft-text",
    },
    failed: {
        icon: CircleX,
        color: "text-danger-surface",
        bg: "bg-danger-soft",
        border: "border-danger-soft",
        dot: "bg-danger-surface",
        badge: "bg-danger-soft text-danger-soft-text",
    },
    crashed: {
        icon: CircleAlert,
        color: "text-danger-surface",
        bg: "bg-danger-soft",
        border: "border-danger-soft",
        dot: "bg-danger-surface",
        badge: "bg-danger-soft text-danger-soft-text",
    },
    stopped: {
        icon: CircleStop,
        color: "text-warning-surface",
        bg: "bg-warning-soft",
        border: "border-warning-soft",
        dot: "bg-warning-surface",
        badge: "bg-warning-soft text-warning-soft-text",
    },
    timeout: {
        icon: TimerOff,
        color: "text-warning-surface",
        bg: "bg-warning-soft",
        border: "border-warning-soft",
        dot: "bg-warning-surface",
        badge: "bg-warning-soft text-warning-soft-text",
    },
    pending: {
        icon: Clock,
        color: "text-on-surface-muted",
        bg: "bg-surface-sunken",
        border: "border-outline",
        dot: "bg-on-surface-faint",
        badge: "bg-surface-sunken text-on-surface",
    },
    ended: {
        icon: CircleDashed,
        color: "text-on-surface-faint",
        bg: "bg-surface-sunken",
        border: "border-outline-faint",
        dot: "bg-on-surface-faint",
        badge: "bg-surface-sunken text-on-surface",
    },
};

export function getRunStatusConfig(status: RunStatus): RunStatusConfig {
    return RUN_STATUS_CONFIG[status];
}

export function runDisplayStatus(run: Pick<Run, "status" | "end_reason">): RunStatus {
    return displayStatus(run.status, run.end_reason);
}
