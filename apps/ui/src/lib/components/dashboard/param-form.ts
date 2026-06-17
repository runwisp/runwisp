// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import type { TaskParam } from "@runwisp/common";

// Pure resolve rules for the manual-trigger parameter form, mirroring the
// daemon's model.ResolveParamValues so the operator gets immediate feedback. The
// component owns the editable state (raw values, overrides, combo mode); these
// functions turn that state into inclusion decisions, field errors, and the
// tri-state supplied value the daemon expects.

export function isComboParam(p: TaskParam): boolean {
    return Boolean(p.choices && p.choices.length > 0 && p.allow_custom);
}

// paramIncluded reports whether a parameter is passed at all. Flags always emit
// (their checkbox is the two-state control) and required params can't be
// dropped; otherwise an explicit operator override wins, falling back to
// "blank → omit, non-empty → include" tracked live against the field content.
export function paramIncluded(p: TaskParam, value: string, override: boolean | undefined): boolean {
    if (p.kind === "flag") return true;
    if (p.required === true) return true;
    if (typeof override === "boolean") return override;
    return value.trim() !== "";
}

// paramSupportsInclude reports whether a field offers the include/omit toggle —
// only free-text fields, where a blank value is ambiguous between "omit" and
// "empty string". Flags already express omit by being off; required params can't
// be omitted; strict selects and numbers can't carry a meaningful empty string.
// A combo qualifies only while sitting on its custom (free-text) slot.
export function paramSupportsInclude(p: TaskParam, inCustomMode: boolean): boolean {
    if (p.kind === "flag" || p.required === true) return false;
    if (p.type === "number") return false;
    if (p.choices && p.choices.length > 0) {
        return Boolean(p.allow_custom) && inCustomMode;
    }
    return true;
}

// Decimal/scientific only — mirrors what the daemon's strconv.ParseFloat accepts
// for typed numbers. Number() would also accept 0x/0o/0b literals, marking
// values valid that the daemon then 400s; this regex rejects them so client and
// server agree.
const NUMBER_RE = /^[+-]?(\d+(\.\d*)?|\.\d+)([eE][+-]?\d+)?$/;
export function isNumberValue(s: string): boolean {
    return NUMBER_RE.test(s.trim());
}

// paramFieldError validates an included value the way the daemon will. An
// omitted field has nothing to validate; an included empty value is only an
// error when the parameter is required.
export function paramFieldError(p: TaskParam, value: string, included: boolean): string {
    if (p.kind === "flag") return "";
    if (!included) return "";
    if (value === "") {
        return p.required === true ? "Required" : "";
    }
    if (
        p.choices &&
        p.choices.length > 0 &&
        p.allow_custom !== true &&
        !p.choices.includes(value)
    ) {
        return `Must be one of: ${p.choices.join(", ")}`;
    }
    if (p.type === "number" && !isNumberValue(value)) {
        return "Must be a number";
    }
    return "";
}

// resolveParamSupplied turns one field's state into the value sent to the
// daemon: a flag carries "true"/"false"; an included value param carries its
// (possibly empty) string; an omitted one carries null so the daemon leaves it
// unset instead of re-injecting the declared default.
export function resolveParamSupplied(
    p: TaskParam,
    value: string,
    included: boolean,
): string | null {
    if (p.kind === "flag") return value === "true" ? "true" : "false";
    return included ? value : null;
}
