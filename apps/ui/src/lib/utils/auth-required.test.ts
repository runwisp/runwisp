// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { afterEach, describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => ({
    markUnauthenticated: vi.fn(),
    emitAuthRequired: vi.fn(),
}));

vi.mock("$app/environment", () => ({ browser: true }));
vi.mock("$lib/stores/auth.svelte", () => ({
    authStore: { markUnauthenticated: h.markUnauthenticated },
}));
vi.mock("$lib/adapters/browser", () => ({
    browserAuthEventBus: { emitAuthRequired: h.emitAuthRequired },
}));

import { authFetch, handleUnauthorized } from "./auth-required";

afterEach(() => {
    vi.unstubAllGlobals();
    vi.clearAllMocks();
});

describe("handleUnauthorized", () => {
    it("marks the session unauthenticated and asks for the login modal", () => {
        handleUnauthorized();
        expect(h.markUnauthenticated).toHaveBeenCalledOnce();
        expect(h.emitAuthRequired).toHaveBeenCalledOnce();
    });
});

describe("authFetch", () => {
    it("routes a 401 response through the handler and still returns it", async () => {
        vi.stubGlobal("fetch", () => Promise.resolve(new Response(null, { status: 401 })));
        const res = await authFetch("/api/runs/x/log");
        expect(res.status).toBe(401);
        expect(h.markUnauthenticated).toHaveBeenCalledOnce();
        expect(h.emitAuthRequired).toHaveBeenCalledOnce();
    });

    it("leaves other statuses alone", async () => {
        vi.stubGlobal("fetch", () => Promise.resolve(new Response("{}", { status: 200 })));
        await authFetch("/api/runs/x/log");
        vi.stubGlobal("fetch", () => Promise.resolve(new Response(null, { status: 500 })));
        await authFetch("/api/runs/x/log");
        expect(h.markUnauthenticated).not.toHaveBeenCalled();
    });
});
