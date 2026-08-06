// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { FAILURE_END_REASONS, END_REASONS } from "@runwisp/common";
import {
    emptyRunFilters,
    NEEDS_ATTENTION_STATUSES,
    isNeedsAttention,
    dimensionActive,
    activeDimensions,
    activeFilterCount,
    clearDimension,
    clearPopoverFilters,
    humanizeStatus,
    triggerDescription,
    FILTERABLE_TRIGGERS,
    statusChipLabel,
    STATUS_BUCKETS,
    bucketState,
    toggleBucket,
    dayStartIso,
    dayEndIso,
    isoToDayInput,
    isWholeDay,
    parseExitCodeRange,
    exitCodeRange,
    exitCodeRangeActive,
    isExitCodeExprValid,
    exitCodeChipLabel,
    type StatusBucket,
    type RunsListFilters,
} from "./run-filters.js";

function bucketByKey(key: string): StatusBucket {
    const bucket = STATUS_BUCKETS.find((b) => b.key === key);
    if (!bucket) throw new Error(`no bucket ${key}`);
    return bucket;
}

const base = (overrides: Partial<RunsListFilters> = {}): RunsListFilters => ({
    ...emptyRunFilters(),
    ...overrides,
});

describe("emptyRunFilters", () => {
    it("is a fully-open filter — no dimension active, newest-first", () => {
        const f = emptyRunFilters();
        expect(f.statuses).toEqual([]);
        expect(f.sortDirection).toBe("desc");
        expect(activeFilterCount(f)).toBe(0);
    });
});

describe("isNeedsAttention", () => {
    it("is true for exactly the attention set, order-insensitive", () => {
        expect(isNeedsAttention([...NEEDS_ATTENTION_STATUSES])).toBe(true);
        expect(isNeedsAttention([...NEEDS_ATTENTION_STATUSES].reverse())).toBe(true);
    });

    it("includes every failure reason plus missed", () => {
        for (const reason of FAILURE_END_REASONS) {
            expect(NEEDS_ATTENTION_STATUSES).toContain(reason);
        }
        expect(NEEDS_ATTENTION_STATUSES).toContain("missed");
    });

    it("is false for a strict subset or a different set", () => {
        expect(isNeedsAttention(["failed"])).toBe(false);
        expect(isNeedsAttention(["success"])).toBe(false);
        expect(isNeedsAttention([])).toBe(false);
    });
});

describe("dimensionActive / activeDimensions", () => {
    it("detects each dimension independently", () => {
        expect(dimensionActive(base({ statuses: ["failed"] }), "status")).toBe(true);
        expect(dimensionActive(base({ createdAfter: "2026-01-01T00:00:00Z" }), "time")).toBe(true);
        expect(dimensionActive(base({ createdBefore: "2026-01-01T00:00:00Z" }), "time")).toBe(true);
        expect(dimensionActive(base({ taskName: "backup" }), "task")).toBe(true);
        expect(dimensionActive(base({ triggeredBy: "cron" }), "triggeredBy")).toBe(true);
        expect(dimensionActive(base({ exitCode: "137" }), "exitCode")).toBe(true);
        expect(dimensionActive(base({ exitCode: ">100" }), "exitCode")).toBe(true);
        expect(dimensionActive(base({ retriesOnly: true }), "retries")).toBe(true);
    });

    it("treats an exact exit code of 0 as active (distinct from absent)", () => {
        expect(dimensionActive(base({ exitCode: "0" }), "exitCode")).toBe(true);
    });

    it("treats an empty or unparseable exit-code expression as inactive", () => {
        expect(dimensionActive(base({ exitCode: "" }), "exitCode")).toBe(false);
        expect(dimensionActive(base({ exitCode: "nonsense" }), "exitCode")).toBe(false);
    });

    it("lists active dimensions in display (most→least useful) order", () => {
        const f = base({
            retriesOnly: true,
            statuses: ["failed"],
            triggeredBy: "cron",
            createdAfter: "2026-01-01T00:00:00Z",
        });
        expect(activeDimensions(f)).toEqual(["status", "time", "triggeredBy", "retries"]);
    });
});

describe("clearDimension", () => {
    it("resets one dimension and leaves the rest untouched", () => {
        const f = base({ statuses: ["failed"], triggeredBy: "cron", retriesOnly: true });
        const cleared = clearDimension(f, "triggeredBy");
        expect(cleared.triggeredBy).toBeUndefined();
        expect(cleared.statuses).toEqual(["failed"]);
        expect(cleared.retriesOnly).toBe(true);
    });

    it("clears the exit-code expression", () => {
        const f = base({ exitCode: ">100 <150" });
        expect(clearDimension(f, "exitCode").exitCode).toBeUndefined();
    });

    it("clears both time bounds together", () => {
        const f = base({ createdAfter: "2026-01-01T00:00:00Z", createdBefore: "2026-02-01" });
        const cleared = clearDimension(f, "time");
        expect(cleared.createdAfter).toBeUndefined();
        expect(cleared.createdBefore).toBeUndefined();
    });
});

describe("clearPopoverFilters", () => {
    it("clears every popover dimension but keeps search and sort", () => {
        const f = base({
            search: "nightly",
            sortDirection: "asc",
            statuses: ["failed"],
            taskName: "backup",
            triggeredBy: "cron",
            exitCode: "1",
            retriesOnly: true,
            createdAfter: "2026-01-01T00:00:00Z",
        });
        const cleared = clearPopoverFilters(f);
        expect(activeFilterCount(cleared)).toBe(0);
        expect(cleared.search).toBe("nightly");
        expect(cleared.sortDirection).toBe("asc");
    });
});

describe("humanizeStatus", () => {
    it("title-cases and de-snakes a status token", () => {
        expect(humanizeStatus("log_overflow")).toBe("Log overflow");
        expect(humanizeStatus("failed")).toBe("Failed");
        expect(humanizeStatus("")).toBe("");
    });
});

describe("statusChipLabel", () => {
    it("names a bucket when the selection matches one exactly", () => {
        // The Failed bucket is the needs-attention set.
        expect(statusChipLabel([...NEEDS_ATTENTION_STATUSES])).toBe("Failed");
        expect(statusChipLabel(["pending", "running"])).toBe("Running");
        expect(statusChipLabel(["success"])).toBe("Succeeded");
    });

    it("humanizes a single status that is not a whole bucket", () => {
        expect(statusChipLabel(["log_overflow"])).toBe("Log overflow");
    });

    it("counts a multi-status set that matches no bucket", () => {
        expect(statusChipLabel(["failed", "crashed"])).toBe("2 statuses");
    });
});

describe("STATUS_BUCKETS", () => {
    it("covers every filterable status exactly once", () => {
        const all = STATUS_BUCKETS.flatMap((b) => [...b.statuses]);
        expect(new Set(all).size).toBe(all.length); // no status in two buckets
        expect(new Set(all)).toEqual(new Set([...END_REASONS, "pending", "running"]));
    });

    it("Failed bucket is the needs-attention set", () => {
        expect(new Set(bucketByKey("failed").statuses)).toEqual(new Set(NEEDS_ATTENTION_STATUSES));
    });
});

describe("bucketState / toggleBucket", () => {
    const failed = bucketByKey("failed");

    it("reads off / partial / on", () => {
        expect(bucketState([], failed)).toBe("off");
        expect(bucketState(["failed"], failed)).toBe("partial");
        expect(bucketState([...failed.statuses], failed)).toBe("on");
    });

    it("selects all of an off or partial bucket", () => {
        const fromOff = toggleBucket(base(), failed);
        expect(new Set(fromOff.statuses)).toEqual(new Set(failed.statuses));

        const fromPartial = toggleBucket(base({ statuses: ["failed"] }), failed);
        expect(new Set(fromPartial.statuses)).toEqual(new Set(failed.statuses));
    });

    it("clears a fully-selected bucket without touching other statuses", () => {
        const f = base({ statuses: [...failed.statuses, "success"] });
        const cleared = toggleBucket(f, failed);
        expect(cleared.statuses).toEqual(["success"]);
    });
});

describe("date helpers", () => {
    it("round-trips a day through its start/end bounds in local time", () => {
        const start = dayStartIso("2016-07-10");
        const end = dayEndIso("2016-07-10");
        expect(isoToDayInput(start ?? "")).toBe("2016-07-10");
        expect(isoToDayInput(end ?? "")).toBe("2016-07-10");
        expect(new Date(start ?? "").getTime()).toBeLessThan(new Date(end ?? "").getTime());
    });

    it("ignores a malformed date", () => {
        expect(dayStartIso("not-a-date")).toBeUndefined();
        expect(dayEndIso("2016-13")).toBeUndefined();
    });

    it("isWholeDay is true when From and To bound the same day", () => {
        // Picking the same date for both ends (start-of-day .. end-of-day) is a
        // whole-day range, so the chip can read "On <date>".
        expect(isWholeDay(dayStartIso("2016-07-10"), dayEndIso("2016-07-10"))).toBe(true);
    });

    it("isWholeDay is false for partial or multi-day ranges", () => {
        expect(isWholeDay(undefined, undefined)).toBe(false);
        expect(isWholeDay(dayStartIso("2016-07-10"), undefined)).toBe(false);
        expect(isWholeDay(dayStartIso("2016-07-10"), dayEndIso("2016-07-12"))).toBe(false);
    });
});

describe("triggerDescription", () => {
    it("gives a fuller label than the row badge", () => {
        expect(triggerDescription("cron")).toBe("Scheduled (cron)");
        expect(triggerDescription("api")).toBe("Manual (UI or API)");
        expect(triggerDescription("service")).toBe("Service auto-start");
        expect(triggerDescription("startup")).toBe("On daemon start");
    });

    it("falls back to a humanized token for anything unknown", () => {
        expect(triggerDescription("whatever_else")).toBe("Whatever else");
    });
});

describe("FILTERABLE_TRIGGERS", () => {
    it("offers every trigger except cloud", () => {
        expect(FILTERABLE_TRIGGERS).toEqual(["cron", "api", "service", "startup"]);
        expect(FILTERABLE_TRIGGERS).not.toContain("cloud");
    });
});

describe("exit-code expression", () => {
    it("parses an exact code as an inclusive [n, n] range", () => {
        expect(parseExitCodeRange("137")).toEqual({ range: { min: 137, max: 137 }, valid: true });
        expect(parseExitCodeRange("-2")).toEqual({ range: { min: -2, max: -2 }, valid: true });
    });

    it("normalizes strict comparisons to inclusive integer bounds", () => {
        expect(exitCodeRange(">100")).toEqual({ min: 101 });
        expect(exitCodeRange(">=100")).toEqual({ min: 100 });
        expect(exitCodeRange("<150")).toEqual({ max: 149 });
        expect(exitCodeRange("<=150")).toEqual({ max: 150 });
    });

    it("combines space-separated tokens into a window", () => {
        expect(exitCodeRange(">100 <150")).toEqual({ min: 101, max: 149 });
        expect(exitCodeRange(">=1 <=255")).toEqual({ min: 1, max: 255 });
    });

    it("treats an empty expression as valid with no bounds", () => {
        expect(parseExitCodeRange("")).toEqual({ range: {}, valid: true });
        expect(parseExitCodeRange("   ")).toEqual({ range: {}, valid: true });
        expect(exitCodeRangeActive("")).toBe(false);
    });

    it("rejects an unparseable token, applying none of it", () => {
        expect(isExitCodeExprValid("12a")).toBe(false);
        expect(isExitCodeExprValid(">>5")).toBe(false);
        expect(exitCodeRange("12a")).toEqual({});
        expect(exitCodeRangeActive("12a")).toBe(false);
    });

    it("reports activity only when a bound resolves", () => {
        expect(exitCodeRangeActive("137")).toBe(true);
        expect(exitCodeRangeActive(">0")).toBe(true);
        expect(exitCodeRangeActive(undefined)).toBe(false);
    });

    it("labels the chip with the raw expression", () => {
        expect(exitCodeChipLabel(">100 <150")).toBe("Exit >100 <150");
        expect(exitCodeChipLabel(" 137 ")).toBe("Exit 137");
    });
});
