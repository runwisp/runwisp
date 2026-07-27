// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import js from "@eslint/js";
import eslintConfigPrettier from "eslint-config-prettier";
import sonarjs from "eslint-plugin-sonarjs";
import tseslint from "typescript-eslint";

const sonarRules = {
    "sonarjs/cognitive-complexity": ["error", 12],
    "sonarjs/no-duplicated-branches": "error",
    "sonarjs/no-all-duplicated-branches": "error",
    "sonarjs/no-identical-conditions": "error",
    "sonarjs/no-identical-expressions": "error",
    "sonarjs/no-identical-functions": "error",
    "sonarjs/no-collapsible-if": "error",
    "sonarjs/no-inverted-boolean-check": "error",
    "sonarjs/no-redundant-boolean": "error",
    "sonarjs/no-useless-catch": "error",
};

const agentsRestrictedSyntaxRules = [
    {
        selector:
            "TSAsExpression:not([typeAnnotation.type='TSConstKeyword']):not([typeAnnotation.type='TSTypeReference'][typeAnnotation.typeName.name='const'])",
        message:
            "Avoid `as` casts. Narrow values explicitly with type guards or validated parsing.",
    },
    {
        selector: "TSTypeAssertion",
        message: "Avoid angle-bracket type assertions. Narrow values explicitly with type guards.",
    },
    {
        selector: "TSNonNullExpression",
        message: "Avoid non-null assertions. Add an explicit guard before using the value.",
    },
    {
        selector: "Program > VariableDeclaration[kind='let']",
        message:
            "Avoid file-scoped mutable state. Move mutable state into a function/class lifecycle or use const.",
    },
    {
        selector: "Program > VariableDeclaration[kind='var']",
        message: "Use const and avoid file-scoped mutable var declarations.",
    },
];

/**
 * A shared ESLint configuration for the repository.
 *
 * @type {import("eslint").Linter.Config[]}
 * */
export const config = [
    js.configs.recommended,
    eslintConfigPrettier,

    ...tseslint.configs.strictTypeChecked.map((config) => ({
        ...config,
        files: ["**/*.{ts,tsx,mts,cts}"],
    })),
    {
        plugins: {
            sonarjs,
        },
        rules: {
            ...sonarRules,
        },
    },
    {
        files: ["**/*.{ts,tsx,mts,cts}"],
        rules: {
            "@typescript-eslint/no-explicit-any": "error",
            "@typescript-eslint/no-non-null-assertion": "error",
            "@typescript-eslint/no-unnecessary-type-assertion": "error",
            "@typescript-eslint/no-unused-vars": [
                "error",
                {
                    argsIgnorePattern: "^_",
                    varsIgnorePattern: "^_",
                    caughtErrorsIgnorePattern: "^_",
                },
            ],
            "@typescript-eslint/no-floating-promises": "error",
            "@typescript-eslint/no-misused-promises": "error",
            "@typescript-eslint/await-thenable": "error",
            "@typescript-eslint/no-unnecessary-condition": "warn",
            "@typescript-eslint/prefer-nullish-coalescing": "warn",
            "@typescript-eslint/prefer-optional-chain": "warn",
            "no-restricted-syntax": ["error", ...agentsRestrictedSyntaxRules],
            "@typescript-eslint/strict-boolean-expressions": [
                "error",
                {
                    allowString: true,
                    allowNullableString: true,
                    allowNumber: false,
                    allowNullableObject: true,
                },
            ],
            "@typescript-eslint/no-deprecated": "error",
        },
    },
    {
        ignores: ["dist/**"],
    },
];
