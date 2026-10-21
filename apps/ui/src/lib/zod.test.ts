// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it, vi } from "vitest";

import { z } from "./zod";

// A CSP without 'unsafe-eval' reports zod's `new Function` capability probe as a
// violation even though the probe catches the throw.
describe("zod under a no-eval CSP", () => {
    it("never touches the Function constructor when building or parsing schemas", () => {
        const probe = vi.spyOn(globalThis, "Function");
        const schema = z.object({ id: z.string(), n: z.number() });
        expect(schema.parse({ id: "a", n: 1 })).toEqual({ id: "a", n: 1 });
        expect(probe).not.toHaveBeenCalled();
        probe.mockRestore();
    });
});
