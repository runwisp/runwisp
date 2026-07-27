// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, it, expect } from "vitest";
import type { TaskParam } from "@runwisp/common";
import {
    paramIncluded,
    paramSupportsInclude,
    paramFieldError,
    resolveParamSupplied,
} from "./param-form";

function opt(over: Partial<TaskParam> = {}): TaskParam {
    return { kind: "option", key: "--note", ...over };
}

describe("paramIncluded", () => {
    it("auto-omits a blank optional field", () => {
        expect(paramIncluded(opt(), "", undefined)).toBe(false);
    });

    it("auto-includes a non-empty optional field", () => {
        expect(paramIncluded(opt(), "hi", undefined)).toBe(true);
    });

    it("treats a whitespace-only field as blank", () => {
        expect(paramIncluded(opt(), "   ", undefined)).toBe(false);
    });

    it("honours an explicit include override on a blank field", () => {
        expect(paramIncluded(opt(), "", true)).toBe(true);
    });

    it("honours an explicit omit override on a filled field", () => {
        expect(paramIncluded(opt(), "hi", false)).toBe(false);
    });

    it("always includes required params and flags", () => {
        expect(paramIncluded(opt({ required: true }), "", undefined)).toBe(true);
        expect(paramIncluded({ kind: "flag", key: "--force" }, "", undefined)).toBe(true);
    });
});

describe("paramSupportsInclude", () => {
    it("offers the toggle for a free-text option", () => {
        expect(paramSupportsInclude(opt(), false)).toBe(true);
    });

    it("withholds it from flags, required, numbers, and strict choices", () => {
        expect(paramSupportsInclude({ kind: "flag", key: "--force" }, false)).toBe(false);
        expect(paramSupportsInclude(opt({ required: true }), false)).toBe(false);
        expect(paramSupportsInclude(opt({ type: "number" }), false)).toBe(false);
        expect(paramSupportsInclude(opt({ choices: ["us", "eu"] }), false)).toBe(false);
    });

    it("offers it for a combo only while on the custom slot", () => {
        const combo = opt({ choices: ["us", "eu"], allow_custom: true });
        expect(paramSupportsInclude(combo, false)).toBe(false);
        expect(paramSupportsInclude(combo, true)).toBe(true);
    });
});

describe("paramFieldError", () => {
    it("does not error on an omitted field", () => {
        expect(paramFieldError(opt({ required: true }), "", false)).toBe("");
    });

    it("flags a required included blank as Required", () => {
        expect(paramFieldError(opt({ required: true }), "", true)).toBe("Required");
    });

    it("accepts an included empty string for an optional free-text field", () => {
        expect(paramFieldError(opt(), "", true)).toBe("");
    });

    it("rejects a value outside strict choices", () => {
        expect(paramFieldError(opt({ choices: ["us", "eu"] }), "ap", true)).toContain("one of");
    });

    it("rejects a non-numeric value for a number param", () => {
        expect(paramFieldError(opt({ type: "number" }), "abc", true)).toBe("Must be a number");
        expect(paramFieldError(opt({ type: "number" }), "0x10", true)).toBe("Must be a number");
        expect(paramFieldError(opt({ type: "number" }), "42", true)).toBe("");
    });
});

describe("resolveParamSupplied", () => {
    it("omits a cleared defaulted field (null, not the default)", () => {
        // The reported bug: clearing a defaulted field must not re-inject it.
        expect(resolveParamSupplied(opt({ default: "hello" }), "", false)).toBeNull();
    });

    it("passes an explicit empty string when force-included", () => {
        expect(resolveParamSupplied(opt(), "", true)).toBe("");
    });

    it("passes the typed value when included", () => {
        expect(resolveParamSupplied(opt(), "world", true)).toBe("world");
    });

    it("canonicalises a flag to true/false regardless of inclusion", () => {
        expect(resolveParamSupplied({ kind: "flag", key: "--force" }, "true", true)).toBe("true");
        expect(resolveParamSupplied({ kind: "flag", key: "--force" }, "", true)).toBe("false");
    });
});
