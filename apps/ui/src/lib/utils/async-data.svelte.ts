// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { toast } from "@runwisp/ui";
import { connectionStore } from "$lib/stores/connection.svelte";

export class AsyncData<T> {
    #data = $state<T | undefined>(undefined);
    #error = $state<string | undefined>(undefined);
    #loading = $state(false);
    #controller: AbortController | null = null;
    readonly #fetcher: (signal: AbortSignal) => Promise<T>;

    // reloadOnReconnect is a test seam: $effect needs a component context.
    constructor(fetcher: (signal: AbortSignal) => Promise<T>, reloadOnReconnect = true) {
        this.#fetcher = fetcher;

        if (reloadOnReconnect) {
            $effect(() => connectionStore.onReconnect(() => void this.fetch()));
        }
    }

    get data(): T | undefined {
        return this.#data;
    }

    get error(): string | undefined {
        return this.#error;
    }

    get loading(): boolean {
        return this.#loading;
    }

    fetch = async (): Promise<void> => {
        this.#controller?.abort();
        const ac = new AbortController();
        this.#controller = ac;

        this.#loading = true;
        this.#error = undefined;
        try {
            const data = await this.#fetcher(ac.signal);
            // A newer fetch() aborts this controller; a fetcher that ignores its
            // signal can still resolve late, so guard the assignment
            // symmetrically with the catch path to keep the freshest data.
            if (ac.signal.aborted) return;
            this.#data = data;
            connectionStore.markConnected();
        } catch (err: unknown) {
            if (ac.signal.aborted) return;
            const message = connectionStore.fetchErrorMessage(err);
            if (message === null) return;
            this.#error = message;
            toast.error(message);
        } finally {
            if (!ac.signal.aborted) {
                this.#loading = false;
            }
        }
    };

    abort = (): void => {
        this.#controller?.abort();
    };
}
