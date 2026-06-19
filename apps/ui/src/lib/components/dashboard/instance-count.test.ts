// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { taskInstanceCount, instanceCountResolver } from "./instance-count.js";

describe("taskInstanceCount", () => {
    it("returns 1 for a non-service task", () => {
        expect(taskInstanceCount({ kind: "task", instances: 3 })).toBe(1);
    });

    it("defaults a service to 1 when instances is unset", () => {
        expect(taskInstanceCount({ kind: "service" })).toBe(1);
    });

    it("returns the configured count for a multi-instance service", () => {
        expect(taskInstanceCount({ kind: "service", instances: 3 })).toBe(3);
    });

    it("floors at 1 for a service configured with fewer than 1 instance", () => {
        expect(taskInstanceCount({ kind: "service", instances: 0 })).toBe(1);
    });
});

describe("instanceCountResolver", () => {
    const resolve = instanceCountResolver([
        { name: "queue-worker", kind: "service", instances: 3 },
        { name: "solo", kind: "service", instances: 1 },
        { name: "nightly", kind: "task", instances: 0 },
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

    it("defaults an unknown task name to 1", () => {
        expect(resolve("ghost")).toBe(1);
    });
});
