// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it, vi } from "vitest";
import type { SSEErrorInfo } from "$lib/utils/event-source";

const h = vi.hoisted(() => ({
    errorHandlers: [] as ((info: SSEErrorInfo) => void)[],
    handleUnauthorized: vi.fn(),
    reportSourceDown: vi.fn(),
}));

vi.mock("./app-stream.svelte", () => ({
    appEventStream: {
        onOpen: () => () => undefined,
        onError: (fn: (info: SSEErrorInfo) => void) => {
            h.errorHandlers.push(fn);
            return () => undefined;
        },
        onStall: () => () => undefined,
        subscribe: () => () => undefined,
    },
}));
vi.mock("$lib/utils/auth-required", () => ({ handleUnauthorized: h.handleUnauthorized }));
vi.mock("./connection.svelte", () => ({
    connectionStore: {
        reportSourceUp: vi.fn(),
        reportSourceDown: h.reportSourceDown,
        reportSourceStalled: vi.fn(),
        releaseSource: vi.fn(),
    },
}));

import { runUpdatesStore } from "./run-updates";

describe("runUpdatesStore stream errors", () => {
    it("treats a 401 as an expired session, not a lost connection", () => {
        runUpdatesStore.connect();
        const onError = h.errorHandlers[0];
        expect(onError).toBeDefined();

        onError?.({ status: 401, message: "unauthorized" });
        expect(h.handleUnauthorized).toHaveBeenCalledOnce();
        expect(h.reportSourceDown).not.toHaveBeenCalled();

        onError?.({ status: 502, message: "bad gateway" });
        expect(h.reportSourceDown).toHaveBeenCalledOnce();
        expect(h.handleUnauthorized).toHaveBeenCalledOnce();
    });
});
