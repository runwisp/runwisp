// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import type { z } from "zod";

export function isRecord(value: unknown): value is Record<string, unknown> {
    return typeof value === "object" && Boolean(value);
}

/** Parse an SSE payload as JSON and validate it against `schema`. Malformed
 * JSON and schema mismatches both come back as a failed result, never a throw. */
export function safeParseJSON<T>(
    data: string,
    schema: z.ZodType<T>,
): { success: true; data: T } | { success: false; error: unknown } {
    let raw: unknown;
    try {
        raw = JSON.parse(data);
    } catch (error) {
        return { success: false, error };
    }
    return schema.safeParse(raw);
}
