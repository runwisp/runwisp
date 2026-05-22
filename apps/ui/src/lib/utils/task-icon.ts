// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import type { Component } from "svelte";
import { Activity, CalendarClock, CircleDot } from "@lucide/svelte";
import { isService, type Task } from "@runwisp/common";

export function taskIcon(task: Task): Component {
    if (isService(task.kind)) return Activity;
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
