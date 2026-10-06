// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

export interface DaemonStats {
    cpuUsage: number;
    memUsage: number;
    activeTasks: number;
    successRate: number;
}
