// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

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
        // CSP for scripts/styles is delivered as a <meta> in each page's <head>
        // (hash mode): SvelteKit computes the hash of its own inline bootstrap
        // and lists it in script-src, so we can drop 'unsafe-inline' for scripts
        // entirely. The daemon's HTTP header (internal/server/routes.go) sets the
        // remaining directives (img/connect/font/frame-ancestors/...) and
        // deliberately does NOT set script-src/style-src, leaving those to this
        // meta so the two policies don't intersect into a broken result.
        csp: {
            mode: "hash",
            directives: {
                "script-src": ["self"],
                "style-src": ["self", "unsafe-inline"],
            },
        },
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
