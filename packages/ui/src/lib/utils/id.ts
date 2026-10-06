// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

/** The last 8 characters of a ULID, the part that differs between runs. */
export function formatShortId(id: string): string {
    return id.slice(-8);
}
