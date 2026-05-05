// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { defineConfig } from "vitest/config";
import path from "node:path";

export default defineConfig({
    test: {
        include: ["src/**/*.test.ts"],
        environment: "node",
    },
    resolve: {
        alias: {
            $lib: path.resolve(__dirname, "./src/lib"),
        },
    },
});
