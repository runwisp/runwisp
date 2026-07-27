// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineConfig } from "vitest/config";
import { svelte, vitePreprocess } from "@sveltejs/vite-plugin-svelte";
import path from "node:path";

export default defineConfig({
    plugins: [
        svelte({
            preprocess: vitePreprocess(),
            compilerOptions: { runes: true },
        }),
    ],
    test: {
        include: [
            "src/**/*.test.ts",
            "../../packages/ui/src/**/*.test.ts",
            "../../packages/common/src/**/*.test.ts",
        ],
        environment: "node",
        server: {
            deps: {
                inline: [/\.svelte\.ts$/, /\.svelte$/],
            },
        },
        coverage: {
            provider: "v8",
            reporter: ["text", "lcov"],
            reportsDirectory: "./coverage",
            allowExternal: true,
            include: [
                "apps/ui/src/**/*.{ts,svelte,svelte.ts}",
                "packages/ui/src/lib/utils/format.ts",
                "packages/ui/src/lib/utils/error.ts",
                "packages/ui/src/lib/utils/id.ts",
                "packages/ui/src/lib/components/dashboard/run-helpers.ts",
                "packages/ui/src/lib/utils/ticking-now.svelte.ts",
                "packages/ui/src/lib/log-console/ansi.ts",
                "packages/common/src/utils/ulid.ts",
            ],
            exclude: [
                "apps/ui/src/**/*.test.ts",
                "apps/ui/src/**/*.spec.ts",
                "apps/ui/test/**",
                "packages/ui/src/**/*.test.ts",
                "packages/common/src/**/*.test.ts",
            ],
        },
    },
    resolve: {
        alias: {
            $lib: path.resolve(__dirname, "./src/lib"),
            "$app/environment": path.resolve(__dirname, "./test/stubs/app-environment.ts"),
            "$app/paths": path.resolve(__dirname, "./test/stubs/app-paths.ts"),
            "$app/navigation": path.resolve(__dirname, "./test/stubs/app-navigation.ts"),
        },
    },
});
