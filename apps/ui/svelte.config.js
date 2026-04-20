// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import adapter from "@sveltejs/adapter-static";
import { vitePreprocess } from "@sveltejs/vite-plugin-svelte";

/** @type {import('@sveltejs/kit').Config} */
const config = {
    preprocess: vitePreprocess(),

    kit: {
        adapter: adapter({
            pages: "build",
            assets: "build",
            fallback: "index.html",
            precompress: false,
            strict: false,
        }),
        paths: {
            base: "",
        },
        prerender: {
            handleMissingId: "ignore",
            handleUnseenRoutes: "ignore",
            handleHttpError: ({ status, path }) => {
                // Ignore 404s for favicon and other missing assets
                if (
                    status === 404 &&
                    (path.includes("favicon") || path.includes(".png") || path.includes(".ico"))
                ) {
                    return;
                }
                throw new Error(`${status} ${path}`);
            },
        },
    },
};

export default config;
