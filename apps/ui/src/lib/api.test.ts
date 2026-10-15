// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { beforeEach, describe, expect, it, vi } from "vitest";

// The generated client captures fetch when api.ts loads and builds Requests
// from relative paths, which Node refuses without a base. Stub both first.
const { fetchMock, onUnauthorized } = vi.hoisted(() => {
    class RelativeRequest extends Request {
        constructor(input: RequestInfo | URL, init?: RequestInit) {
            super(typeof input === "string" ? new URL(input, "http://runwisp.test") : input, init);
        }
    }
    const fetchMock = vi.fn<(input: RequestInfo | URL, init?: RequestInit) => Promise<Response>>();
    vi.stubGlobal("Request", RelativeRequest);
    vi.stubGlobal("fetch", fetchMock);
    return { fetchMock, onUnauthorized: vi.fn() };
});

vi.mock("./utils/auth-required", () => ({ handleUnauthorized: onUnauthorized }));

const { AuthRequiredError, RateLimitedError, authApi, requestError, runsApi, systemApi, tasksApi } =
    await import("./api");

function json(body: unknown, init: ResponseInit = {}): Response {
    return new Response(JSON.stringify(body), {
        ...init,
        headers: { "Content-Type": "application/json" },
    });
}

/** The method and path+query of the nth fetch call. */
function sent(n = 0): string {
    const input = fetchMock.mock.calls[n]?.[0];
    if (!(input instanceof Request)) throw new Error(`call ${String(n)} was not a Request`);
    const url = new URL(input.url);
    return `${input.method} ${url.pathname}${url.search}`;
}

beforeEach(() => {
    fetchMock.mockReset();
    onUnauthorized.mockReset();
});

describe("requestError", () => {
    it("surfaces the server's huma detail", async () => {
        const res = new Response(
            JSON.stringify({ title: "Bad Request", status: 400, detail: "tasks.x: bad cron" }),
            { status: 400, statusText: "Bad Request" },
        );
        expect((await requestError(res)).message).toBe("tasks.x: bad cron");
    });

    it("falls back to the status line for a non-JSON body", async () => {
        const res = new Response("oops", { status: 502, statusText: "Bad Gateway" });
        expect((await requestError(res)).message).toBe("Request failed: 502 Bad Gateway");
    });

    it("falls back to the status line when detail is missing", async () => {
        const res = new Response(JSON.stringify({ title: "x" }), {
            status: 500,
            statusText: "Internal Server Error",
        });
        expect((await requestError(res)).message).toBe("Request failed: 500 Internal Server Error");
    });
});

describe("generated client", () => {
    it("rejects with the server's detail on a failed call", async () => {
        fetchMock.mockResolvedValue(json({ detail: "reload rejected: bad cron" }, { status: 400 }));
        await expect(systemApi.reload()).rejects.toThrow("reload rejected: bad cron");
    });

    it("hands a 401 to the login flow and rejects with AuthRequiredError", async () => {
        fetchMock.mockResolvedValue(new Response(null, { status: 401 }));
        await expect(tasksApi.getAll()).rejects.toBeInstanceOf(AuthRequiredError);
        expect(onUnauthorized).toHaveBeenCalledTimes(1);
    });

    it("rejects an empty success body instead of resolving undefined", async () => {
        fetchMock.mockResolvedValue(new Response(null, { status: 204 }));
        await expect(systemApi.getInfo()).rejects.toThrow("Empty API response");
    });

    it("defaults missing list fields to empty arrays", async () => {
        fetchMock.mockResolvedValue(json({}));
        await expect(tasksApi.getAll()).resolves.toEqual([]);
        fetchMock.mockResolvedValue(json({}));
        await expect(systemApi.getMetricsHistory()).resolves.toEqual([]);
        fetchMock.mockResolvedValue(json({}));
        await expect(runsApi.bulkRerun({ ids: ["r1"] })).resolves.toEqual({ triggered: [] });
    });

    it("tags a trigger as from the UI and only sends params when there are some", async () => {
        fetchMock.mockImplementation(() => Promise.resolve(json({ id: "r1" })));
        await tasksApi.triggerRun("backup", {});
        await tasksApi.triggerRun("backup", { ORG_ID: "7" });
        expect(sent(0)).toBe("POST /api/tasks/backup/run?via=ui");
        const [first, second] = fetchMock.mock.calls.map(([input]) => input);
        if (!(first instanceof Request) || !(second instanceof Request))
            throw new Error("no request");
        expect(await first.text()).toBe("");
        expect(await second.json()).toEqual({ params: { ORG_ID: "7" } });
    });

    it("posts per-task actions to their own endpoint", async () => {
        fetchMock.mockImplementation(() => Promise.resolve(new Response(null, { status: 204 })));
        await tasksApi.pauseSchedule("backup");
        await tasksApi.restartService("web");
        await tasksApi.stopRun("r9");
        expect([sent(0), sent(1), sent(2)]).toEqual([
            "POST /api/tasks/backup/pause",
            "POST /api/tasks/web/restart",
            "POST /api/runs/r9/stop",
        ]);
    });

    it("passes run list filters through and returns runs with the total", async () => {
        fetchMock.mockResolvedValue(json({ items: [{ id: "r1" }], total: 41 }));
        await expect(runsApi.getAll({ taskName: "backup", limit: 20 })).resolves.toEqual({
            runs: [{ id: "r1" }],
            total: 41,
        });
        expect(sent()).toBe("GET /api/runs?taskName=backup&limit=20");
    });

    it("turns null frames of a line's history into empty ones", async () => {
        fetchMock.mockResolvedValue(json({ frames: [["10%"], null] }));
        await expect(tasksApi.getLogLineHistory("r1", 3)).resolves.toEqual([["10%"], []]);
        expect(sent()).toBe("GET /api/runs/r1/log/line/3/history");
    });
});

describe("authApi", () => {
    it("answers the challenge and returns the granted role", async () => {
        fetchMock
            .mockResolvedValueOnce(json({ nonce: "00ff" }))
            .mockResolvedValueOnce(json({ readOnly: true }));
        await expect(authApi.login("pw")).resolves.toEqual({ readOnly: true });
        const body = fetchMock.mock.calls[1]?.[1]?.body;
        if (typeof body !== "string") throw new Error("login sent no body");
        const sentBody: unknown = JSON.parse(body);
        expect(sentBody).toHaveProperty("nonce", "00ff");
        expect(sentBody).toHaveProperty("response");
    });

    it("reports throttling with the server's wait", async () => {
        fetchMock.mockResolvedValue(
            new Response(null, { status: 429, headers: { "Retry-After": "30" } }),
        );
        const err = await authApi.login("pw").catch((e: unknown) => e);
        expect(err).toBeInstanceOf(RateLimitedError);
        expect(err).toHaveProperty("retryAfterSeconds", 30);
    });

    it("tells a wrong password apart from throttling", async () => {
        fetchMock
            .mockResolvedValueOnce(json({ nonce: "00ff" }))
            .mockResolvedValueOnce(new Response(null, { status: 401 }));
        await expect(authApi.login("wrong")).rejects.toThrow("Authentication failed");
    });

    it("parses the auth status", async () => {
        const status = { authRequired: true, authenticated: false, readOnly: false };
        fetchMock.mockResolvedValue(json(status));
        await expect(authApi.status()).resolves.toEqual(status);
        fetchMock.mockResolvedValue(new Response(null, { status: 500 }));
        await expect(authApi.status()).rejects.toThrow("Failed to check auth status");
    });
});
