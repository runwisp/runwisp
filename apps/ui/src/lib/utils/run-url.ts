// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { goto } from "$app/navigation";

/**
 * Navigate to `target` — a resolve()d route to the selected run (`/runs/{id}` or
 * `/tasks/{name}/{id}`), or the bare list/task page when nothing is selected — so
 * the address bar is always a copy-pasteable permalink to the run on screen.
 *
 * - No-ops when already on `target`: this absorbs the initial echo on load (the
 *   page seeds its selection from the URL, then reports it straight back), so a
 *   companion `?line=` deep link survives until the user picks a different run.
 * - Switching runs drops any `?line` highlight — it belongs to the run it
 *   arrived with, and navigating to a query-less path clears it.
 * - `replaceState` so clicking through runs never bloats browser history; the
 *   address bar still always reflects the current run.
 */
export function navigateToRun(currentUrl: URL, target: string): void {
    if (target === currentUrl.pathname) return;
    // `target` is always a resolve()d pathname supplied by the caller; the lint
    // rule can't trace that through the parameter, so silence its false positive.
    // eslint-disable-next-line svelte/no-navigation-without-resolve
    void goto(target, { replaceState: true, keepFocus: true, noScroll: true });
}
