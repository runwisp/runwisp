import type { StorybookConfig } from "@storybook/sveltekit";

import { dirname } from "path";

import { fileURLToPath } from "url";

function getAbsolutePath(value: string): string {
    return dirname(fileURLToPath(import.meta.resolve(`${value}/package.json`)));
}
const config: StorybookConfig = {
    stories: ["../src/**/*.mdx", "../src/**/*.stories.@(js|ts|svelte)"],
    addons: [
        getAbsolutePath("@storybook/addon-svelte-csf"),
        getAbsolutePath("@chromatic-com/storybook"),
        getAbsolutePath("@storybook/addon-a11y"),
        getAbsolutePath("@storybook/addon-docs"),
    ],
    framework: getAbsolutePath("@storybook/sveltekit"),
};
export default config;
