// Copyright (c) PoppyCake, s.r.o. SPDX-License-Identifier: Apache-2.0

export interface DaemonState {
    pid: number;
    port: number;
    dataDir: string;
    password: string;
    token: string;
}
