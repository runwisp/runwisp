// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { requestError } from "./api";

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
