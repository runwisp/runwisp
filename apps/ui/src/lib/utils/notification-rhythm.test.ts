// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { z } from "zod";
import { phrase, sparkline } from "./notification-rhythm";
import vectorsRaw from "./__rhythm_vectors.json";

const vectorSchema = z.object({
    name: z.string(),
    count: z.number(),
    created_at: z.string(),
    last_occurred_at: z.string(),
    occurrences_unix_ms: z.array(z.number()),
    now_unix_ms: z.number(),
    window_ms: z.number(),
    cells: z.number(),
    expected_phrase: z.string(),
    expected_sparkline: z.string(),
});

const vectors = z.array(vectorSchema).parse(vectorsRaw);

describe("notification rhythm parity (Go ↔ TS)", () => {
    for (const v of vectors) {
        it(v.name, () => {
            const now = new Date(v.now_unix_ms);
            const occ = v.occurrences_unix_ms.map((ms) => new Date(ms));
            const phraseOut = phrase({
                count: v.count,
                createdAt: v.created_at,
                lastOccurredAt: v.last_occurred_at,
                occurrences: occ,
                now,
            });
            expect(phraseOut).toBe(v.expected_phrase);
            if (v.expected_sparkline !== "") {
                const sparkOut = sparkline(occ, now, v.window_ms, v.cells);
                expect(sparkOut).toBe(v.expected_sparkline);
            }
        });
    }
});
