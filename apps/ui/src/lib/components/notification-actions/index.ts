// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import type { Component } from "svelte";
import type { Notification } from "$lib/stores/notifications.svelte";
import UpdateAvailableAction from "./UpdateAvailableAction.svelte";

export interface NotificationActionProps {
    notification: Notification;
}

// Maps a notification kind to an inline action component rendered inside the
// notification row (bell popover + full list). Add a kind here to give its
// notifications an interactive control; the component owns its own state and
// calls whatever specific endpoint the action needs.
const notificationActions = new Map<string, Component<NotificationActionProps>>([
    ["update.available", UpdateAvailableAction],
]);

export function notificationActionFor(
    kind: string,
): Component<NotificationActionProps> | undefined {
    return notificationActions.get(kind);
}
