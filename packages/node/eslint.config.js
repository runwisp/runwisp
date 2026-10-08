// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { config as baseConfig } from "@runwisp/eslint-config/base";

/** @type {import("eslint").Linter.Config[]} */
export default [
    ...baseConfig,
    {
        ignores: ["src/generated/**"],
    },
    {
        languageOptions: {
            parserOptions: {
                projectService: true,
                tsconfigRootDir: import.meta.dirname,
            },
        },
    },
];
