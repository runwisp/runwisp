// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

/** The added/removed/changed counts a reload reports; mirrors model.ReloadResult. */
export interface ReloadCounts {
    added?: string[] | null;
    removed?: string[] | null;
    changed?: unknown[] | null;
}

// reloadSummary renders a one-line summary of what a reload changed, mirroring
// the TUI's reloadSummary (internal/tui/model_update.go) so the two surfaces
// report a reload the same way.
export function reloadSummary(result: ReloadCounts): string {
    const added = result.added?.length ?? 0;
    const removed = result.removed?.length ?? 0;
    const changed = result.changed?.length ?? 0;
    const parts: string[] = [];
    if (added > 0) parts.push(`+${String(added)} added`);
    if (removed > 0) parts.push(`-${String(removed)} removed`);
    if (changed > 0) parts.push(`~${String(changed)} changed`);
    return parts.length === 0
        ? "Config reloaded — no changes"
        : `Config reloaded: ${parts.join(", ")}`;
}
