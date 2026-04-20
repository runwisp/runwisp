// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

/** Shared sort utilities for runs. */

interface HasCreatedAt {
    created_at: string;
}

export function sortByCreatedAtDesc<T extends HasCreatedAt>(items: T[]): T[] {
    return [...items].sort(
        (left, right) => new Date(right.created_at).getTime() - new Date(left.created_at).getTime(),
    );
}
