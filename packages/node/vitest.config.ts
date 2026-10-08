// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { defineConfig } from "vitest/config";

// e2e only: unit tests in src/ run (with coverage) from apps/ui/vitest.config.ts.
export default defineConfig({
    test: {
        include: ["e2e/**/*.test.ts"],
        testTimeout: 30_000,
        hookTimeout: 30_000,
    },
});
