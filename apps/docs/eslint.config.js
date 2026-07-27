// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { fileURLToPath } from "node:url";
import { includeIgnoreFile } from "@eslint/compat";
import { defineConfig } from "eslint/config";
import astro from "eslint-plugin-astro";
import { config as baseConfig } from "@runwisp/eslint-config/base";

const gitignorePath = fileURLToPath(new URL("./.gitignore", import.meta.url));

export default defineConfig(
    includeIgnoreFile(gitignorePath),
    {
        ignores: [
            "**/*.config.*",
            "**/*.cjs",
            ".astro/**",
            "dist/**",
            "node_modules/**",
            "public/**",
        ],
    },
    ...baseConfig,
    ...astro.configs.recommended,
    {
        files: ["**/*.{ts,mts,cts}"],
        languageOptions: {
            parserOptions: { projectService: true },
        },
    },
);
