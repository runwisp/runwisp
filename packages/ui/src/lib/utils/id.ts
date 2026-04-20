// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

export function formatShortId(id: string, length = 8): string {
    return id.slice(-length);
}
