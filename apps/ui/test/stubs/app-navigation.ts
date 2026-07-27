// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Vitest stub for `$app/navigation` (SvelteKit-only at runtime).
export function goto(_url: string | URL, _opts?: Record<string, unknown>): Promise<void> {
    return Promise.resolve();
}
