// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { createSvelteConfig } from "@runwisp/eslint-config/svelte";
import { fileURLToPath } from "node:url";
import { includeIgnoreFile } from "@eslint/compat";
import { defineConfig } from "eslint/config";
import svelteConfig from "./svelte.config.js";

const gitignorePath = fileURLToPath(new URL("./.gitignore", import.meta.url));

export default defineConfig(
    includeIgnoreFile(gitignorePath),
    ...createSvelteConfig({
        svelteConfig,
        extraIgnores: [
            "**/*.config.*",
            "**/*.cjs",
            ".svelte-kit/**",
            "build/**",
            "e2e/**",
            "node_modules/**",
            "scripts/**",
            "test-results/**",
        ],
    }),
);
