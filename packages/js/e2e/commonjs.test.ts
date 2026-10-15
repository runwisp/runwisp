// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";

import { describe, expect, it } from "vitest";

const APP = fileURLToPath(new URL("./fixtures/nestjs.cjs", import.meta.url));

describe("@runwisp/js/nestjs from CommonJS", () => {
    // Nest compiles to CommonJS by default: the built ES module must load
    // through require(), named imports from @nestjs/common included.
    it("boots a CommonJS Nest app with the built package", () => {
        const out = execFileSync(process.execPath, [APP], { encoding: "utf8" });
        expect(JSON.parse(out)).toEqual([
            ["Jobs.nightly", "0 3 * * *"],
            ["Jobs.poll", "@every 30s"],
            ["warm-up", ""],
        ]);
    });
});
