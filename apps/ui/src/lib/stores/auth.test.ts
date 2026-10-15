// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it, vi } from "vitest";

const { status } = vi.hoisted(() => ({ status: vi.fn() }));

vi.mock("$app/environment", () => ({ browser: true }));
vi.mock("$lib/api", () => ({ authApi: { status } }));

const { authStore } = await import("./auth.svelte");

describe("authStore", () => {
    it("counts a daemon without auth as signed in", async () => {
        status.mockResolvedValue({ authRequired: false, authenticated: false, readOnly: false });
        await authStore.load();
        expect(authStore.current).toEqual({
            required: false,
            loaded: true,
            authenticated: true,
            readOnly: false,
        });
    });

    it("takes the session and role from the server", async () => {
        status.mockResolvedValue({ authRequired: true, authenticated: true, readOnly: true });
        await authStore.load();
        expect(authStore.current).toMatchObject({ authenticated: true, readOnly: true });
    });

    it("falls back to signed out when the status call fails", async () => {
        status.mockRejectedValue(new Error("offline"));
        await authStore.load();
        expect(authStore.current).toEqual({
            required: true,
            loaded: true,
            authenticated: false,
            readOnly: false,
        });
    });

    it("tracks a sign-in and a lost session", () => {
        authStore.markAuthenticated(true);
        expect(authStore.current).toMatchObject({ authenticated: true, readOnly: true });
        authStore.markUnauthenticated();
        expect(authStore.current).toMatchObject({ authenticated: false, readOnly: false });
    });
});
