// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import type { Task as CommonTask, Run as CommonRun } from "@runwisp/common";

/**
 * Web UI component types.
 * Source of truth for status values: {@link RunStatus} from `@runwisp/common`.
 */

export type Task = CommonTask;

export type Run = CommonRun;

export interface DaemonState {
    name: string;
    version: string;
    uptime: string;
    status: "connected" | "disconnected";
    host: string;
    backendUrl: string;
    cpus: number;
    memory: string;
    os: string;
    arch: string;
    workDir: string;
    fingerprint: string;
}

export interface DaemonStats {
    cpuUsage: number;
    memUsage: number;
    activeTasks: number;
    successRate: number;
}
