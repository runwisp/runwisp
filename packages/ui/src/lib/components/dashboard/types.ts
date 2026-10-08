// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

export interface DaemonStats {
    cpuUsage: number;
    memUsage: number;
    activeTasks: number;
    successRate: number;
}

/** A single output-search hit surfaced under its run in the history rail. */
export interface RunOutputMatch {
    line: number;
    text: string;
}
