// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { fileURLToPath } from "node:url";
import { defineConfig, includeIgnoreFile } from "eslint/config";
import astro from "eslint-plugin-astro";
import svelte from "eslint-plugin-svelte";
import ts from "typescript-eslint";
import { config as baseConfig } from "@runwisp/eslint-config/base";
import svelteConfig from "./svelte.config.js";

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
    ...svelte.configs.recommended,
    {
        files: ["**/*.{ts,mts,cts}"],
        languageOptions: {
            parserOptions: { projectService: true },
        },
    },
    {
        files: ["**/*.svelte"],
        languageOptions: {
            parserOptions: {
                extraFileExtensions: [".svelte"],
                parser: ts.parser,
                svelteConfig,
            },
        },
        // Islands run in the browser; core no-undef doesn't know browser globals
        // (location, document, requestAnimationFrame). svelte-check covers real
        // undefined references. Mirrors @runwisp/eslint-config/svelte.
        //
        // no-navigation-without-resolve is a SvelteKit rule (expects resolve()
        // from $app/paths); this is an Astro island whose only links are
        // same-page #hash anchors, so it's a false positive here.
        rules: {
            "no-undef": "off",
            "svelte/no-navigation-without-resolve": "off",
        },
    },
);
