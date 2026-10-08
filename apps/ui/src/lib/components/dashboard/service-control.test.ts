// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { isServiceStopped } from "./service-control.js";

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
