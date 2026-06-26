// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// Vitest stub for `$app/navigation` (SvelteKit-only at runtime).
export function goto(_url: string | URL, _opts?: Record<string, unknown>): Promise<void> {
    return Promise.resolve();
}
