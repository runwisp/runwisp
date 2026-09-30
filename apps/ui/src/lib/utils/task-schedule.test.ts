// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from "vitest";
import type { Task } from "@runwisp/common";
import { canTogglePause, showScheduleChip } from "./task-schedule";

function makeTask(overrides: Partial<Task> = {}): Task {
    return {
        name: "nightly",
        cron: "0 3 * * *",
        manualTrigger: true,
        autostart: false,
        runOnStart: false,
        ...overrides,
    };
}

describe("showScheduleChip", () => {
    it("shows for a cron task while the daemon schedules", () => {
        expect(showScheduleChip(makeTask(), true)).toBe(true);
    });

    it("hides for services, cron-less tasks, and station mode", () => {
        expect(showScheduleChip(makeTask({ kind: "service" }), true)).toBe(false);
        expect(showScheduleChip(makeTask({ cron: "" }), true)).toBe(false);
        expect(showScheduleChip(makeTask(), false)).toBe(false);
    });
});

describe("canTogglePause", () => {
    it("allows a manual_trigger task", () => {
        expect(canTogglePause(makeTask())).toBe(true);
    });

    it("refuses a task locked with manual_trigger = false", () => {
        expect(canTogglePause(makeTask({ manualTrigger: false }))).toBe(false);
    });

    it("refuses to pause a held task but lets a paused one resume", () => {
        expect(canTogglePause(makeTask({ heldBy: "cron" }))).toBe(false);
        expect(canTogglePause(makeTask({ heldBy: "cron", pausedAt: "2026-09-29T14:00:00Z" }))).toBe(
            true,
        );
    });
});
