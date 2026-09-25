// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// Declarative filter spec. A `FilterField[]` + one bound `FilterValues` object
// drives <FilterBar>, the client engine (`applyFilters`), and the URL sync
// helper — the same way DataGrid's `Column[]` drives the table. Add a field
// type here, and every table that adopts it renders identically.

import type { SelectOption } from "./Select.svelte";

// `path` (where present) is the dot-path read on the row for client-side
// matching; it defaults to `key`. `key` is also the slot in `FilterValues` and
// the URL query param. Search fields test `fields` (dot-paths) instead.
export type FilterField =
    | {
          type: "search";
          key: string;
          fields: string[];
          placeholder?: string;
          primary?: boolean;
      }
    | {
          type: "select";
          key: string;
          label: string;
          options: SelectOption[];
          path?: string;
          primary?: boolean;
      }
    | {
          type: "number";
          key: string;
          label: string;
          placeholder?: string;
          path?: string;
          primary?: boolean;
      }
    | {
          // Stored as two values: `${key}From` / `${key}To` (yyyy-mm-dd).
          type: "daterange";
          key: string;
          label: string;
          path?: string;
          primary?: boolean;
      };

// All values are strings — DOM inputs are string-valued and URL params are
// strings; the client engine coerces (e.g. Number()) where a field needs it.
export type FilterValues = Record<string, string>;

// The full state a list emits via onQueryChange: FilterBar values + sort +
// pagination. The host mirrors this to the URL; server-mode lists also
// map it to REST query params.
export interface TableQuery {
    values: FilterValues;
    sortKey?: string;
    sortDir?: "asc" | "desc";
    page: number;
    pageSize: number;
}

// A select value is "inactive" (matches everything) when blank, undefined, or
// the conventional "all" sentinel some existing option lists use.
export function isBlank(v: string | undefined): boolean {
    return !v || v === "all";
}

// Which value keys a field owns — daterange owns two, everything else one.
// Used by chips, active-state detection, clear, and URL (de)serialization so
// none of them has to special-case the range type.
export function fieldKeys(field: FilterField): string[] {
    return field.type === "daterange" ? [`${field.key}From`, `${field.key}To`] : [field.key];
}

export function isFieldActive(field: FilterField, values: FilterValues): boolean {
    return fieldKeys(field).some((k) => !isBlank(values[k]));
}

// Human label for a field. Every type but `search` carries one; search has no
// label (its box speaks for itself), so fall back to the key for the narrower.
export function fieldLabel(field: FilterField): string {
    return "label" in field ? field.label : field.key;
}

// Seed a values object with every field's keys present (blank), overlaid with
// any initial values (e.g. from the URL). Keeping all keys present up front
// keeps the emitted query reactive to each field and the bindings simple.
export function seedValues(fields: FilterField[], initial: FilterValues = {}): FilterValues {
    const values: FilterValues = {};
    for (const f of fields) {
        for (const k of fieldKeys(f)) values[k] = initial[k] ?? "";
    }
    return values;
}
