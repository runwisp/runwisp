// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

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
        include: ["src/**/*.test.ts"],
        environment: "node",
        server: {
            deps: {
                inline: [/\.svelte\.ts$/, /\.svelte$/],
            },
        },
    },
    resolve: {
        alias: {
            $lib: path.resolve(__dirname, "./src/lib"),
            "$app/environment": path.resolve(__dirname, "./test/stubs/app-environment.ts"),
            "$app/paths": path.resolve(__dirname, "./test/stubs/app-paths.ts"),
        },
    },
});
