// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { toast, extractErrorMessage } from "@runwisp/ui";
import { AuthRequiredError } from "$lib/api";
import { connectionStore } from "$lib/stores/connection.svelte";

export interface AsyncDataOptions {
    toastOnError?: boolean;
    reloadOnReconnect?: boolean;
}

export class AsyncData<T> {
    #data = $state<T | undefined>(undefined);
    #error = $state<string | undefined>(undefined);
    #loading = $state(false);
    #controller: AbortController | null = null;
    readonly #fetcher: (signal: AbortSignal) => Promise<T>;
    readonly #toastOnError: boolean;

    constructor(fetcher: (signal: AbortSignal) => Promise<T>, options: AsyncDataOptions = {}) {
        const { toastOnError = true, reloadOnReconnect = true } = options;
        this.#fetcher = fetcher;
        this.#toastOnError = toastOnError;

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
            if (err instanceof AuthRequiredError) return;
            const isConnectionErr = connectionStore.reportFetchError(err);
            const message = isConnectionErr ? "Connection lost" : extractErrorMessage(err);
            this.#error = message;
            if (this.#toastOnError) {
                toast.error(message);
            }
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
