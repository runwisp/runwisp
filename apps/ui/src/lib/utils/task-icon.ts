// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import type { Component } from "svelte";
import { AppWindow, CalendarClock, CircleDot } from "@lucide/svelte";
import { isService, type Task } from "@runwisp/common";

export function taskIcon(task: Task): Component {
    if (isService(task.kind)) return AppWindow;
    if (task.cron && task.cron.trim() !== "") return CalendarClock;
    return CircleDot;
}

export function taskTriggerTooltip(task: Task): string {
    if (isService(task.kind)) {
        const instances = task.instances ?? 1;
        return instances > 1 ? `Service × ${String(instances)}` : "Service";
    }
    if (task.cron && task.cron.trim() !== "") return `Cron · ${task.cron}`;
    return "Manual trigger";
}
