// Copyright (c) PoppyCake, s.r.o. SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { humanizeCron } from "./cron-format.js";

describe("humanizeCron", () => {
    it("humanizes a standard 5-field expression", () => {
        const result = humanizeCron("*/5 * * * *");
        expect(result.humanized).toBe("Every 5 minutes");
        expect(result.raw).toBe("*/5 * * * *");
        expect(result.isHumanized).toBe(true);
    });

    it("humanizes a daily expression", () => {
        const result = humanizeCron("0 3 * * *");
        expect(result.humanized).toBe("At 03:00 AM");
        expect(result.isHumanized).toBe(true);
    });

    it("humanizes @hourly via cronstrue", () => {
        const result = humanizeCron("@hourly");
        expect(result.humanized).toBe("Every hour");
        expect(result.isHumanized).toBe(true);
    });

    it("handles robfig's @every with a compound duration", () => {
        const result = humanizeCron("@every 1h30m");
        expect(result.humanized).toBe("Every 1 hour 30 minutes");
        expect(result.raw).toBe("@every 1h30m");
        expect(result.isHumanized).toBe(true);
    });

    it("handles @every with a single unit", () => {
        expect(humanizeCron("@every 10s").humanized).toBe("Every 10 seconds");
        expect(humanizeCron("@every 1m").humanized).toBe("Every 1 minute");
        expect(humanizeCron("@every 2h").humanized).toBe("Every 2 hours");
    });

    it("skips zero-valued duration segments", () => {
        expect(humanizeCron("@every 1h0m").humanized).toBe("Every 1 hour");
    });

    it("falls back to raw for @every with sub-second units", () => {
        const result = humanizeCron("@every 500ms");
        expect(result.humanized).toBe("@every 500ms");
        expect(result.isHumanized).toBe(false);
    });

    it("falls back to raw for @every with a garbage duration", () => {
        const result = humanizeCron("@every banana");
        expect(result.humanized).toBe("@every banana");
        expect(result.isHumanized).toBe(false);
    });

    it("falls back to raw for unparseable expressions — never 'Invalid'", () => {
        const result = humanizeCron("not a cron");
        expect(result.humanized).toBe("not a cron");
        expect(result.isHumanized).toBe(false);
        expect(result.humanized).not.toMatch(/invalid/i);
    });

    it("trims surrounding whitespace", () => {
        const result = humanizeCron("  */5 * * * *  ");
        expect(result.raw).toBe("*/5 * * * *");
        expect(result.isHumanized).toBe(true);
    });
});
