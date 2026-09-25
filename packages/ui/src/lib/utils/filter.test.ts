// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { applyFilters, matchesQuery, resolvePath } from "./filter.js";
import {
    type FilterField,
    fieldKeys,
    fieldLabel,
    isBlank,
    isFieldActive,
    seedValues,
} from "../components/filter-spec.js";

interface Row {
    id: string;
    status: string;
    exitCode: number;
    roles: string[];
    user: { name: string };
    startedAt: Date;
}

const rows: Row[] = [
    {
        id: "a",
        status: "ok",
        exitCode: 0,
        roles: ["owner"],
        user: { name: "Ada" },
        startedAt: new Date("2026-01-10"),
    },
    {
        id: "b",
        status: "err",
        exitCode: 1,
        roles: ["viewer"],
        user: { name: "Bob" },
        startedAt: new Date("2026-02-20"),
    },
    {
        id: "c",
        status: "ok",
        exitCode: 2,
        roles: ["owner", "viewer"],
        user: { name: "Cy" },
        startedAt: new Date("2026-03-30"),
    },
];

const range: FilterField = { type: "daterange", key: "startedAt", label: "Date" };
const search: FilterField = { type: "search", key: "q", fields: ["id", "user.name"] };
const status: FilterField = { type: "select", key: "status", label: "Status", options: [] };

const fields: FilterField[] = [
    search,
    status,
    { type: "select", key: "role", label: "Role", path: "roles", options: [] },
    { type: "number", key: "exitCode", label: "Exit" },
    range,
];

const ids = (r: Row[]) => r.map((x) => x.id);

describe("applyFilters", () => {
    it("passes everything through when values are empty", () => {
        expect(applyFilters(rows, fields, {})).toHaveLength(3);
    });

    it("searches dot-paths case-insensitively", () => {
        expect(ids(applyFilters(rows, fields, { q: "ad" }))).toEqual(["a"]);
    });

    it("matches select by equality and treats 'all' as inactive", () => {
        expect(ids(applyFilters(rows, fields, { status: "ok" }))).toEqual(["a", "c"]);
        expect(applyFilters(rows, fields, { status: "all" })).toHaveLength(3);
    });

    it("matches select against an array cell by membership", () => {
        expect(ids(applyFilters(rows, fields, { role: "viewer" }))).toEqual(["b", "c"]);
    });

    it("coerces number values", () => {
        expect(ids(applyFilters(rows, fields, { exitCode: "1" }))).toEqual(["b"]);
        expect(ids(applyFilters(rows, fields, { exitCode: "0" }))).toEqual(["a"]);
    });

    it("filters a date range inclusively on both ends", () => {
        expect(
            ids(
                applyFilters(rows, fields, {
                    startedAtFrom: "2026-02-01",
                    startedAtTo: "2026-03-30",
                }),
            ),
        ).toEqual(["b", "c"]);
    });

    it("ANDs combined filters", () => {
        expect(ids(applyFilters(rows, fields, { status: "ok", role: "viewer" }))).toEqual(["c"]);
    });

    it("coerces non-string select cells (number, boolean, date) and skips the rest", () => {
        interface Flagged {
            id: string;
            flags: unknown[];
        }
        const flaggedRows: Flagged[] = [
            { id: "a", flags: [1, true, new Date("2026-01-01T00:00:00.000Z"), {}] },
            { id: "b", flags: [0, false] },
        ];
        const flagField: FilterField = {
            type: "select",
            key: "flag",
            label: "Flag",
            path: "flags",
            options: [],
        };
        const flagIds = (r: Flagged[]) => r.map((x) => x.id);
        expect(flagIds(applyFilters(flaggedRows, [flagField], { flag: "1" }))).toEqual(["a"]);
        expect(flagIds(applyFilters(flaggedRows, [flagField], { flag: "true" }))).toEqual(["a"]);
        expect(
            flagIds(
                applyFilters(flaggedRows, [flagField], {
                    flag: new Date("2026-01-01T00:00:00.000Z").toISOString(),
                }),
            ),
        ).toEqual(["a"]);
        expect(applyFilters(flaggedRows, [flagField], { flag: "no-such-value" })).toHaveLength(0);
    });
});

describe("filter helpers", () => {
    it("matchesQuery skips nullish fields and matches empty queries", () => {
        expect(matchesQuery("", null)).toBe(true);
        expect(matchesQuery("x", null, undefined)).toBe(false);
        expect(matchesQuery("OB", "bob")).toBe(true);
    });

    it("resolvePath reads nested fields and misses gracefully", () => {
        expect(resolvePath(rows[0], "user.name")).toBe("Ada");
        expect(resolvePath(rows[0], "user.missing.deep")).toBeUndefined();
    });

    it("isBlank treats blank, undefined, and 'all' as inactive", () => {
        expect(isBlank(undefined)).toBe(true);
        expect(isBlank("")).toBe(true);
        expect(isBlank("all")).toBe(true);
        expect(isBlank("0")).toBe(false);
    });

    it("fieldLabel uses the label, falling back to the key for search fields", () => {
        expect(fieldLabel(status)).toBe("Status");
        expect(fieldLabel(search)).toBe("q");
    });

    it("daterange owns two value keys", () => {
        expect(fieldKeys(range)).toEqual(["startedAtFrom", "startedAtTo"]);
        expect(isFieldActive(range, { startedAtTo: "2026-01-01" })).toBe(true);
    });

    it("seedValues fills every key and overlays initial values", () => {
        expect(seedValues(fields, { status: "ok", extra: "dropped" })).toEqual({
            q: "",
            status: "ok",
            role: "",
            exitCode: "",
            startedAtFrom: "",
            startedAtTo: "",
        });
    });
});
