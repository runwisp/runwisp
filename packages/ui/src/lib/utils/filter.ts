// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { type FilterField, type FilterValues, isBlank } from "../components/filter-spec.js";

// Case-insensitive substring match against any of the given fields. Empty
// query matches everything; nullish fields are skipped.
export function matchesQuery(query: string, ...fields: (string | null | undefined)[]): boolean {
    if (!query) return true;
    const q = query.toLowerCase();
    return fields.some((f) => f?.toLowerCase().includes(q) === true);
}

// Dot-path aware field read: resolves "user.name" against nested objects.
// Shared by DataGrid's sort comparator and the filter engine below so both
// read fields the same way.
export function resolvePath(obj: unknown, path: string): unknown {
    return path
        .split(".")
        .reduce<unknown>(
            (acc, k): unknown =>
                typeof acc === "object" && acc !== null ? Reflect.get(acc, k) : undefined,
            obj,
        );
}

// Scalar cell → the string a filter value is compared against. Objects and
// nullish cells have no text form, so they never match a search or select.
function toText(v: unknown): string | null {
    if (typeof v === "string") return v;
    if (typeof v === "number" || typeof v === "boolean" || typeof v === "bigint") return String(v);
    if (v instanceof Date) return v.toISOString();
    return null;
}

// Array cell (e.g. member roles) → membership; scalar → equality.
function matchSelect(cell: unknown, v: string): boolean {
    return Array.isArray(cell) ? cell.some((c) => toText(c) === v) : toText(cell) === v;
}

// Inclusive yyyy-mm-dd range. The ISO date portion sorts lexically, so a
// string compare is enough.
function matchDateRange(cell: unknown, from: string | undefined, to: string | undefined): boolean {
    if (isBlank(from) && isBlank(to)) return true;
    const d = cell instanceof Date ? cell : new Date(toText(cell) ?? "");
    if (Number.isNaN(d.getTime())) return false;
    const day = d.toISOString().slice(0, 10);
    return (isBlank(from) || day >= (from ?? "")) && (isBlank(to) || day <= (to ?? ""));
}

function matchField(row: unknown, field: FilterField, values: FilterValues): boolean {
    switch (field.type) {
        case "search":
            return matchesQuery(
                values[field.key] ?? "",
                ...field.fields.map((p) => toText(resolvePath(row, p))),
            );
        case "select": {
            const v = values[field.key];
            return isBlank(v) || matchSelect(resolvePath(row, field.path ?? field.key), v ?? "");
        }
        case "number": {
            const v = values[field.key];
            return isBlank(v) || Number(resolvePath(row, field.path ?? field.key)) === Number(v);
        }
        case "daterange":
            return matchDateRange(
                resolvePath(row, field.path ?? field.key),
                values[`${field.key}From`],
                values[`${field.key}To`],
            );
    }
}

// Client-side filter engine: a row survives when every field matches.
// Server-mode tables map the same `values` to query params instead.
export function applyFilters<T>(rows: T[], fields: FilterField[], values: FilterValues): T[] {
    return rows.filter((row) => fields.every((f) => matchField(row, f, values)));
}
