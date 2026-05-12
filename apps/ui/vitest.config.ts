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
                // @mzattahri/srp ships extensionless ESM imports that node's
                // resolver rejects; inlining routes the module through vite's
                // bundler, which handles the missing extensions like Vite
                // does for the production build.
                inline: [/\.svelte\.ts$/, /\.svelte$/, /@mzattahri\/srp/],
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
