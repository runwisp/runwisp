// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { browser } from "$app/environment";

const TICK_MS = 30_000;

function createClockStore() {
    let now = $state(Date.now());
    let timer: ReturnType<typeof setInterval> | null = null;

    function start(): void {
        if (!browser || timer !== null) return;
        now = Date.now();
        timer = setInterval(() => {
            now = Date.now();
        }, TICK_MS);
    }

    function stop(): void {
        if (timer !== null) {
            clearInterval(timer);
            timer = null;
        }
    }

    if (browser) start();

    return {
        get now(): number {
            return now;
        },
        start,
        stop,
    };
}

export const clockStore = createClockStore();
