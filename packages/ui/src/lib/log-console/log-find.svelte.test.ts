// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { compileFind, LogFind, mergeLines, type FindPage } from "./log-find.svelte";

function finder(q: string, regex = false, caseSensitive = false) {
    const f = compileFind(q, regex, caseSensitive);
    if (!f || "error" in f) throw new Error("expected a finder");
    return f;
}

describe("compileFind", () => {
    it("is null for an empty query", () => {
        expect(compileFind("", false, false)).toBeNull();
    });

    it("matches substrings case-insensitively by default, special characters literally", () => {
        expect(finder("ERR").test("an error")).toBe(true);
        expect(finder("ERR", false, true).test("an error")).toBe(false);
        expect(finder("a.b").test("axb")).toBe(false);
        expect(finder("a.b").test("a.b")).toBe(true);
    });

    it("matches text that colour codes split", () => {
        expect(finder("error: boom").test("\x1b[31merror\x1b[0m: boom")).toBe(true);
    });

    it("returns every non-empty range", () => {
        expect(finder("o").ranges("foo bo")).toEqual([
            [1, 2],
            [2, 3],
            [5, 6],
        ]);
        expect(finder("x*", true).ranges("axxb")).toEqual([[1, 3]]);
    });

    it("jumps to the first and last match, and pages by a tenth or 10", async () => {
        const { find } = pages({ items: Array.from({ length: 300 }, (_, n) => ({ n })) });
        find.query = "x";
        await find.search("run");
        expect(find.current).toBe(0);
        find.page(1);
        expect(find.current).toBe(30);
        find.page(-1);
        find.page(-1);
        expect(find.current).toBe(0);
        find.last();
        expect(find.current).toBe(299);
        find.page(1);
        expect(find.current).toBe(299);
        find.first();
        expect(find.current).toBe(0);

        const few = pages({ items: [{ n: 1 }, { n: 2 }, { n: 3 }] });
        few.find.query = "x";
        await few.find.search("run");
        few.find.page(1);
        expect(few.find.current).toBe(3);
    });

    it("reports a broken regex", () => {
        expect(compileFind("(", true, false)).toHaveProperty("error");
    });
});

describe("mergeLines", () => {
    it("merges ascending lists without duplicates", () => {
        expect(mergeLines([1, 4, 9], [2, 4, 10])).toEqual([1, 2, 4, 9, 10]);
    });
});

function pages(...list: FindPage[]) {
    const calls: Array<string | undefined> = [];
    const find = new LogFind((_runId, query) => {
        calls.push(query.cursor);
        const page = list.shift();
        return page ? Promise.resolve(page) : Promise.reject(new Error("no more pages"));
    });
    find.open = true;
    return { find, calls };
}

describe("LogFind", () => {
    it("pages through the search and selects the first match", async () => {
        const { find, calls } = pages(
            { items: [{ n: 3 }, { n: 7 }], nextCursor: "c1" },
            { items: [{ n: 12 }] },
        );
        find.query = "x";
        await find.search("run");
        expect(calls).toEqual([undefined, "c1"]);
        expect(find.matches).toEqual([3, 7, 12]);
        expect(find.current).toBe(3);
        expect(find.status).toBe("done");
    });

    it("adds streamed lines without moving the selection, and dedupes them", async () => {
        const { find } = pages({ items: [{ n: 1 }, { n: 4 }] });
        find.query = "hit";
        await find.search("run");
        find.ingest({ 4: "hit", 6: "miss", 8: "\x1b[1mHIT\x1b[0m" });
        expect(find.matches).toEqual([1, 4, 8]);
        expect(find.current).toBe(1);
    });

    it("wraps next and prev", async () => {
        const { find } = pages({ items: [{ n: 1 }, { n: 4 }] });
        find.query = "x";
        await find.search("run");
        find.next();
        expect(find.current).toBe(4);
        find.next();
        expect(find.current).toBe(1);
        find.prev();
        expect(find.current).toBe(4);
    });

    it("stops at the page cap, drops streamed matches past it, and stops live matching", async () => {
        const full = Array.from({ length: 10 }, (_, i) => ({
            items: [{ n: i }],
            nextCursor: `c${String(i)}`,
        }));
        const { find } = pages(...full);
        find.query = "x";
        const searching = find.search("run");
        find.ingest({ 500: "x" });
        await searching;
        expect(find.capped).toBe(true);
        expect(find.matches).toEqual([0, 1, 2, 3, 4, 5, 6, 7, 8, 9]);
        find.ingest({ 600: "x" });
        expect(find.matches).not.toContain(600);
    });

    it("surfaces a failed search and ignores a superseded one", async () => {
        const { find } = pages();
        find.query = "x";
        await find.search("run");
        expect(find.status).toBe("error");
        expect(find.error).toBe("no more pages");

        let resolveFirst: (p: FindPage) => void = () => {};
        const slow = new LogFind((_r, q) =>
            q.q === "old"
                ? new Promise<FindPage>((r) => (resolveFirst = r))
                : Promise.resolve({ items: [{ n: 2 }] }),
        );
        slow.open = true;
        slow.query = "old";
        const first = slow.search("run");
        slow.query = "new";
        await slow.search("run");
        resolveFirst({ items: [{ n: 99 }] });
        await first;
        expect(slow.matches).toEqual([2]);
    });

    it("jumps to the first and last match, and pages by a tenth or 10", async () => {
        const { find } = pages({ items: Array.from({ length: 300 }, (_, n) => ({ n })) });
        find.query = "x";
        await find.search("run");
        expect(find.current).toBe(0);
        find.page(1);
        expect(find.current).toBe(30);
        find.page(-1);
        find.page(-1);
        expect(find.current).toBe(0);
        find.last();
        expect(find.current).toBe(299);
        find.page(1);
        expect(find.current).toBe(299);
        find.first();
        expect(find.current).toBe(0);

        const few = pages({ items: [{ n: 1 }, { n: 2 }, { n: 3 }] });
        few.find.query = "x";
        await few.find.search("run");
        few.find.page(1);
        expect(few.find.current).toBe(3);
    });

    it("reports a broken regex without calling the daemon", async () => {
        const { find, calls } = pages();
        find.regex = true;
        find.query = "(";
        await find.search("run");
        expect(find.status).toBe("error");
        expect(calls).toEqual([]);
    });
});
