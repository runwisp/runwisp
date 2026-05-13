// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { copyFileSync, existsSync, mkdirSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const source = resolve(here, "../../runwisp/openapi.json");
const destination = resolve(here, "../public/openapi.json");

if (!existsSync(source)) {
    console.error(`sync-openapi: source not found at ${source}`);
    console.error("Run `make generate` (or `cd apps/runwisp && bun run openapi`) first.");
    process.exit(1);
}

mkdirSync(dirname(destination), { recursive: true });
copyFileSync(source, destination);
console.log(`sync-openapi: ${source} → ${destination}`);
