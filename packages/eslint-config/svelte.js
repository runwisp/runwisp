// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import prettier from "eslint-config-prettier";
import svelte from "eslint-plugin-svelte";
import globals from "globals";
import ts from "typescript-eslint";

import { config as baseConfig } from "./base.js";

/**
 * Creates a shared ESLint config for Svelte packages.
 *
 * @param {{ svelteConfig: unknown, extraIgnores?: string[] }} options
 * @returns {import("eslint").Linter.Config[]}
 */
export function createSvelteConfig({ svelteConfig, extraIgnores = [] }) {
    return [
        ...baseConfig,
        ...svelte.configs.recommended,
        prettier,
        ...svelte.configs.prettier,
        { ignores: extraIgnores },
        {
            languageOptions: {
                globals: { ...globals.browser, ...globals.node },
            },
            rules: { "no-undef": "off" },
        },
        {
            files: ["**/*.ts"],
            languageOptions: {
                parserOptions: { projectService: true },
            },
        },
        {
            files: ["**/*.svelte", "**/*.svelte.ts", "**/*.svelte.js"],
            plugins: { "@typescript-eslint": ts.plugin },
            languageOptions: {
                parserOptions: {
                    projectService: true,
                    extraFileExtensions: [".svelte"],
                    parser: ts.parser,
                    svelteConfig,
                },
            },
            rules: {
                "@typescript-eslint/no-deprecated": "error",
                "@typescript-eslint/no-floating-promises": "error",
                "@typescript-eslint/no-misused-promises": "error",
                "no-unused-vars": "off",
            },
        },
    ];
}
