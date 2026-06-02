// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { fileURLToPath } from "node:url";

import tailwindcss from "@tailwindcss/vite";
import { sveltekit } from "@sveltejs/kit/vite";
import { defineConfig } from "vite";

// Resolve @runwisp/ui to this repo's packages/ui source directly. Bun-workspace
// node_modules symlinks normally point @runwisp/ui at the same checkout, but in
// a git worktree (e.g. Cline's) node_modules is borrowed from the main checkout
// — which may sit on a different branch — so the bare specifier would compile
// stale components while Tailwind scans the worktree's (current) sources. The
// build's make dependency graph already assumes packages/ui (UI_LIB_SOURCES);
// this alias makes module resolution honor that.
const uiLib = fileURLToPath(new URL("../../packages/ui/src/lib", import.meta.url));

export default defineConfig({
    plugins: [tailwindcss(), sveltekit()],
    resolve: {
        alias: [
            { find: /^@runwisp\/ui$/, replacement: `${uiLib}/index.ts` },
            { find: /^@runwisp\/ui\/(.*)$/, replacement: `${uiLib}/$1` },
        ],
    },
    server: {
        port: 3000,
        strictPort: false,
    },
});
