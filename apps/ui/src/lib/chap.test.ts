// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { describe, it, expect } from "vitest";
import { chapResponse } from "./chap";

// These vectors are the cross-language contract with the Go side
// (apps/runwisp/internal/chap/chap_test.go). The same (password, nonce) →
// response pairs are asserted there; if either implementation changes the KDF
// formula, iteration count, salt, or output length, one suite breaks and forces
// them back in sync.
const vectors = [
    {
        name: "doc example",
        password: "password",
        nonce: "nonce",
        want: "736b9e46edb32dbde0382c376f35dd8cf79b8338cff806a9cc2fccf588b08c44",
    },
    {
        name: "hex nonce",
        password: "hunter2",
        nonce: "0123456789abcdef",
        want: "52e61d8f37e5294458ac2225126008ca1e32c7847b228221d574dc305f40f8ce",
    },
];

describe("chapResponse", () => {
    for (const v of vectors) {
        it(`matches the Go vector: ${v.name}`, async () => {
            expect(await chapResponse(v.password, v.nonce)).toBe(v.want);
        });
    }

    it("depends on the nonce", async () => {
        const a = await chapResponse("pw", "nonce-a");
        const b = await chapResponse("pw", "nonce-b");
        expect(a).not.toBe(b);
    });
});
