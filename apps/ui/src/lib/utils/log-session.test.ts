// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it, vi } from "vitest";
import { makeRun } from "../../../test/fixtures";

const { api, logs } = vi.hoisted(() => ({
    api: {
        getLogPage: vi.fn(() => Promise.resolve({ page: "raw" })),
        getLogLineHistory: vi.fn(() => Promise.resolve([["frame"]])),
    },
    logs: {
        parseLogPage: vi.fn(() => ({ lines: { 3: "parsed" }, sizeLines: 4, finished: false })),
        streamRunLog: vi.fn(() => () => undefined),
    },
}));

vi.mock("$lib/api", () => ({ tasksApi: api }));
vi.mock("$lib/logs", () => logs);

const { createLogSession } = await import("./log-session");

const known = makeRun("r1");
const session = createLogSession((id) => (id === known.id ? known : undefined));

describe("createLogSession", () => {
    it("fetches and parses a page for a known run, sized from the requested range", async () => {
        await expect(session.fetchLogs("r1", 3, 7)).resolves.toEqual({
            lines: { 3: "parsed" },
            sizeLines: 4,
            finished: false,
        });
        expect(api.getLogPage).toHaveBeenCalledWith("r1", { from: 3, limit: 5 });
    });

    it("answers an unknown run with an empty finished page, without fetching", async () => {
        api.getLogPage.mockClear();
        await expect(session.fetchLogs("gone", 0, 9)).resolves.toEqual({
            lines: {},
            sizeLines: 0,
            finished: true,
        });
        expect(api.getLogPage).not.toHaveBeenCalled();
    });

    it("streams and reads line history only for a known run", async () => {
        const onEvent = vi.fn();
        session.streamLogs("gone", onEvent);
        expect(logs.streamRunLog).not.toHaveBeenCalled();
        session.streamLogs("r1", onEvent);
        expect(logs.streamRunLog).toHaveBeenCalledWith("r1", onEvent, undefined);

        await expect(session.fetchLineHistory("gone", 2)).resolves.toEqual([]);
        await expect(session.fetchLineHistory("r1", 2)).resolves.toEqual([["frame"]]);
    });
});
