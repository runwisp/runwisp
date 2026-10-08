// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
    runDuration,
    runStartDelay,
    runVerdict,
    runEndMarker,
    runRowReadout,
    highlightParts,
    formatTriggeredByLabel,
    runRetryLabel,
    runUsageLabel,
    instanceSuffix,
    runLogDownloadUrl,
} from "./run-helpers.js";

describe("runLogDownloadUrl", () => {
    it("points at the daemon's raw log route", () => {
        expect(runLogDownloadUrl("01J0000000000000000000000A")).toBe(
            "/api/runs/01J0000000000000000000000A/log/raw",
        );
    });
});

describe("runEndMarker", () => {
    it("warns only for ends the operator or daemon caused", () => {
        expect(runEndMarker("stopped")).toEqual({
            label: "run stopped by operator",
            tone: "warn",
        });
        expect(runEndMarker("daemon_stopped").tone).toBe("warn");
        expect(runEndMarker("timeout").label).toBe("run timed out");
        expect(runEndMarker("failed")).toEqual({ label: "end of output", tone: "muted" });
    });
});

describe("runVerdict", () => {
    it("phrases a timed outcome so a duration reads after it", () => {
        expect(runVerdict("succeeded")).toEqual({ verb: "succeeded in", timed: true });
        expect(runVerdict("failed")).toEqual({ verb: "failed after", timed: true });
    });

    it("marks statuses that never produced a duration as untimed", () => {
        // These end without ever running, so the caller renders the verb alone
        // rather than "skipped after —".
        expect(runVerdict("missed").timed).toBe(false);
        expect(runVerdict("skipped").timed).toBe(false);
        expect(runVerdict("dst_skipped").timed).toBe(false);
        expect(runVerdict("queue_full").timed).toBe(false);
        expect(runVerdict("pending").timed).toBe(false);
    });
});

describe("runDuration", () => {
    it("returns undefined when startedAt is not set", () => {
        expect(runDuration({})).toBeUndefined();
    });

    it("returns formatted duration when both startedAt and endedAt are set", () => {
        const start = "2024-06-15T12:00:00.000Z";
        const end = "2024-06-15T12:00:05.000Z";
        expect(runDuration({ startedAt: start, endedAt: end })).toBe("5s");
    });

    it("returns ms duration for sub-second runs", () => {
        const start = "2024-06-15T12:00:00.000Z";
        const end = "2024-06-15T12:00:00.500Z";
        expect(runDuration({ startedAt: start, endedAt: end })).toBe("500ms");
    });

    describe("with fake clock", () => {
        beforeEach(() => {
            vi.useFakeTimers();
            vi.setSystemTime(new Date("2024-06-15T12:00:02.000Z"));
        });
        afterEach(() => {
            vi.useRealTimers();
        });

        it("uses current time when endedAt is not set (run still in progress)", () => {
            expect(runDuration({ startedAt: "2024-06-15T12:00:00.000Z" })).toBe("2s");
        });
    });

    it("counts against an injected now for an in-progress run", () => {
        const start = "2024-06-15T12:00:00.000Z";
        const now = new Date("2024-06-15T12:00:07.000Z").getTime();
        expect(runDuration({ startedAt: start }, now)).toBe("7s");
    });

    it("ignores the injected now once the run has ended", () => {
        const start = "2024-06-15T12:00:00.000Z";
        const end = "2024-06-15T12:00:05.000Z";
        const now = new Date("2024-06-15T12:01:00.000Z").getTime();
        expect(runDuration({ startedAt: start, endedAt: end }, now)).toBe("5s");
    });
});

describe("runStartDelay", () => {
    it("returns undefined when startedAt is not set", () => {
        expect(runStartDelay({ createdAt: "2024-06-15T12:00:00.000Z" })).toBeUndefined();
    });

    it("returns undefined when the run started within a second of its tick", () => {
        expect(
            runStartDelay({
                createdAt: "2024-06-15T12:00:00.000Z",
                startedAt: "2024-06-15T12:00:00.300Z",
            }),
        ).toBeUndefined();
    });

    it("formats the jitter/queue gap when the run started meaningfully later", () => {
        expect(
            runStartDelay({
                createdAt: "2024-06-15T03:00:00.000Z",
                startedAt: "2024-06-15T03:07:12.000Z",
            }),
        ).toBe("7m 12s");
    });
});

describe("formatTriggeredByLabel", () => {
    it("humanizes each trigger source", () => {
        expect(formatTriggeredByLabel("api")).toBe("API");
        expect(formatTriggeredByLabel("ui")).toBe("UI");
        expect(formatTriggeredByLabel("cli")).toBe("CLI");
        expect(formatTriggeredByLabel("cron")).toBe("Cron");
        expect(formatTriggeredByLabel("service")).toBe("Service");
        expect(formatTriggeredByLabel("startup")).toBe("Startup");
        expect(formatTriggeredByLabel("station")).toBe("Station");
    });
});

describe("runUsageLabel", () => {
    it("is undefined for an unmeasured run", () => {
        expect(runUsageLabel({})).toBeUndefined();
    });

    it("joins peak memory and CPU time", () => {
        expect(runUsageLabel({ peakMemoryBytes: 48 * 1024 * 1024, cpuTimeMs: 1500 })).toBe(
            "peak 48 MB · CPU 1s",
        );
    });

    it("shows whichever was measured", () => {
        expect(runUsageLabel({ cpuTimeMs: 0 })).toBe("CPU 0ms");
    });
});

describe("runRetryLabel", () => {
    it("returns undefined for a first attempt that is not a retry", () => {
        expect(runRetryLabel({ retryAttempt: 0 })).toBeUndefined();
    });

    it("labels a run with a positive attempt number", () => {
        expect(runRetryLabel({ retryAttempt: 2 })).toBe("retry #2");
    });

    it("labels a run that points back at the run it re-attempts", () => {
        expect(runRetryLabel({ retryAttempt: 1, retryOfRunId: "01JABC" })).toBe("retry #1");
    });
});

describe("instanceSuffix", () => {
    it("returns no suffix for a single-instance task", () => {
        expect(instanceSuffix(0, 1)).toBe("");
    });

    it("returns no suffix for a non-service (count 0)", () => {
        expect(instanceSuffix(0, 0)).toBe("");
    });

    it("suffixes slot 0 of a multi-instance service as #1 (1-based)", () => {
        expect(instanceSuffix(0, 3)).toBe("#1");
    });

    it("maps the stored 0-based slot to a 1-based suffix", () => {
        expect(instanceSuffix(1, 3)).toBe("#2");
        expect(instanceSuffix(2, 3)).toBe("#3");
    });
});

describe("runRowReadout", () => {
    const ended = { startedAt: "2026-01-01T00:00:00Z", endedAt: "2026-01-01T00:00:02Z" };

    it("shows live, queued and the exit code of a failure", () => {
        expect(runRowReadout({ ...ended, status: "running", exitCode: 0 }, "running")).toBe("live");
        expect(runRowReadout({ ...ended, status: "pending", exitCode: 0 }, "pending")).toBe(
            "queued",
        );
        expect(runRowReadout({ ...ended, status: "ended", exitCode: 3 }, "failed")).toBe("exit 3");
    });

    it("falls back to the duration, or a dash when it never started", () => {
        expect(runRowReadout({ ...ended, status: "ended", exitCode: 0 }, "succeeded")).toBe("2s");
        expect(runRowReadout({ status: "ended", exitCode: 0 }, "missed")).toBe("—");
    });
});

describe("highlightParts", () => {
    it("returns the whole line when nothing matches", () => {
        expect(highlightParts("hello", "zzz")).toEqual({ before: "hello", match: "", after: "" });
        expect(highlightParts("hello", "  ")).toEqual({ before: "hello", match: "", after: "" });
    });

    it("splits around a case-insensitive match", () => {
        expect(highlightParts("an ERROR here", "error")).toEqual({
            before: "an ",
            match: "ERROR",
            after: " here",
        });
    });

    it("windows a long lead-in with an ellipsis", () => {
        const parts = highlightParts("x".repeat(40) + "needle", "needle");
        expect(parts.before).toBe("…" + "x".repeat(14));
        expect(parts.match).toBe("needle");
    });
});
