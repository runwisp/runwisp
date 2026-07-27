// Copyright (c) PoppyCake, s.r.o. SPDX-License-Identifier: GPL-3.0-or-later

export interface DaemonState {
    pid: number;
    port: number;
    dataDir: string;
    password: string;
    token: string;
}
