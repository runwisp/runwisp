// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { AppWindow, PowerOff } from "@lucide/svelte";
import type { Task } from "@runwisp/common";
import {
    canTogglePause,
    instanceCountResolver,
    isServiceStopped,
    serviceLabel,
    taskIcon,
    taskTriggerTooltip,
} from "./task";

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

describe("instanceCountResolver", () => {
    const resolve = instanceCountResolver([
        { name: "queue-worker", kind: "service", instances: 3 },
        { name: "solo", kind: "service", instances: 1 },
        { name: "nightly", kind: "task", instances: 0 },
        { name: "empty", kind: "service", instances: 0 },
    ]);

    it("resolves a multi-instance service to its count", () => {
        expect(resolve("queue-worker")).toBe(3);
    });

    it("resolves a single-instance service to 1", () => {
        expect(resolve("solo")).toBe(1);
    });

    it("resolves a non-service to 1", () => {
        expect(resolve("nightly")).toBe(1);
    });

    it("floors a service configured with fewer than 1 instance to 1", () => {
        expect(resolve("empty")).toBe(1);
    });

    it("defaults an unknown task name to 1", () => {
        expect(resolve("ghost")).toBe(1);
    });
});

describe("serviceLabel", () => {
    it("names the instance count only for a multi-instance service", () => {
        expect(serviceLabel({ kind: "service", instances: 3 })).toBe("Service ×3");
        expect(serviceLabel({ kind: "service" })).toBe("Service");
    });
});

describe("isServiceStopped", () => {
    it("is true for a service the daemon reports stopped, so a reload still offers Start", () => {
        expect(isServiceStopped({ kind: "service", serviceStopped: true })).toBe(true);
    });

    it("is false for a running or restarting service, which offers Restart", () => {
        expect(isServiceStopped({ kind: "service" })).toBe(false);
        expect(isServiceStopped({ kind: "service", serviceStopped: false })).toBe(false);
    });

    it("is false for a task", () => {
        expect(isServiceStopped({ kind: "task", serviceStopped: true })).toBe(false);
    });
});

describe("taskIcon", () => {
    it("marks a stopped service in the sidebar", () => {
        const svc = makeTask({ kind: "service" });
        expect(taskIcon({ ...svc, serviceStopped: true })).toBe(PowerOff);
        expect(taskIcon(svc)).toBe(AppWindow);
        expect(taskTriggerTooltip({ ...svc, serviceStopped: true })).toBe("Service · stopped");
    });
});
