// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { SvelteDate } from "svelte/reactivity";

const DEFAULT_TICK_INTERVAL_MS = 30_000;

// TickingNow is a reactive clock that advances on a fixed interval, so
// relative-time labels re-render without waiting for a data event.
// Call start() inside an $effect and return its cleanup.
export class TickingNow {
    #ms = $state(Date.now());
    readonly #intervalMs: number;

    constructor(intervalMs: number = DEFAULT_TICK_INTERVAL_MS) {
        this.#intervalMs = intervalMs;
    }

    start(): () => void {
        this.#ms = Date.now();
        const timer = setInterval(() => {
            this.#ms = Date.now();
        }, this.#intervalMs);
        return () => {
            clearInterval(timer);
        };
    }

    get now(): Date {
        return new SvelteDate(this.#ms);
    }
}
