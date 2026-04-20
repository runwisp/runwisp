// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { toast, extractErrorMessage } from "@runwisp/ui";
import { AuthRequiredError } from "$lib/api";

export interface AsyncData<T> {
    readonly data: T | undefined;
    readonly error: string | undefined;
    readonly loading: boolean;
    fetch(): Promise<void>;
    reload(): Promise<void>;
    abort(): void;
}

export function createAsyncData<T>(
    fetcher: (signal: AbortSignal) => Promise<T>,
    options: { toastOnError?: boolean } = {},
): AsyncData<T> {
    const { toastOnError = true } = options;

    let data = $state<T | undefined>(undefined);
    let error = $state<string | undefined>(undefined);
    let loading = $state(false);
    let controller: AbortController | null = null;

    async function doFetch() {
        controller?.abort();
        const ac = new AbortController();
        controller = ac;

        loading = true;
        error = undefined;
        try {
            data = await fetcher(ac.signal);
        } catch (err: unknown) {
            if (ac.signal.aborted) return;
            if (err instanceof AuthRequiredError) return;
            const message = extractErrorMessage(err);
            error = message;
            if (toastOnError) {
                toast.error(message);
            }
        } finally {
            if (!ac.signal.aborted) {
                loading = false;
            }
        }
    }

    return {
        get data() {
            return data;
        },
        get error() {
            return error;
        },
        get loading() {
            return loading;
        },
        fetch: doFetch,
        reload: doFetch,
        abort() {
            controller?.abort();
        },
    };
}
