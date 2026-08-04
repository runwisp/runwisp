// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

/** Shared sort utilities for runs. */

interface HasCreatedAt {
    createdAt: string;
}

export function sortByCreatedAtDesc<T extends HasCreatedAt>(items: T[]): T[] {
    return [...items].sort(
        (left, right) => new Date(right.createdAt).getTime() - new Date(left.createdAt).getTime(),
    );
}
