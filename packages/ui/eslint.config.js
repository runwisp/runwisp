// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { createSvelteConfig } from "@runwisp/eslint-config/svelte";
import { fileURLToPath } from "node:url";
import { defineConfig, includeIgnoreFile } from "eslint/config";
import svelteConfig from "./svelte.config.js";

const gitignorePath = fileURLToPath(new URL("./.gitignore", import.meta.url));

export default defineConfig(
    includeIgnoreFile(gitignorePath),
    { ignores: [".storybook/", "vite.config.ts", "svelte.config.js"] },
    ...createSvelteConfig({ svelteConfig }),
    { files: ["src/lib/**/*.svelte"], rules: { "svelte/no-navigation-without-resolve": "off" } },
    { files: ["**/*.cjs"], rules: { "@typescript-eslint/no-require-imports": "off" } },
);
