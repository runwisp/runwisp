// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { instanceCountResolver } from "./instance-count.js";

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
