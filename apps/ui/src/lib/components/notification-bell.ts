// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import type { Notification } from "$lib/stores";

// The bell's badge renders in the "error" color only when something has
// actually failed — i.e. at least one UNREAD notification is severity
// "error". Extracted so the per-item unread+severity check is unit testable
// without a component-render harness.

/** Whether any unread notification is severity "error". */
export function hasUnreadError(items: readonly Notification[]): boolean {
    return items.some((n) => n.severity === "error" && !n.readAt);
}
