// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

/**
 * Extracts a human-readable message from an unknown caught value.
 */
export function extractErrorMessage(
    err: unknown,
    fallback = "An unexpected error occurred",
): string {
    if (err instanceof Error) {
        return err.message || fallback;
    }
    if (typeof err === "string") {
        return err || fallback;
    }
    return fallback;
}
