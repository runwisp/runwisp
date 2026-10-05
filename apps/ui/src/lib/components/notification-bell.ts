// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import type { Notification } from "$lib/stores";

/** Whether any unread notification is severity "error", which turns the bell's badge red. */
export function hasUnreadError(items: readonly Notification[]): boolean {
    return items.some((n) => n.severity === "error" && !n.readAt);
}
