// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

export function toTaskPageId(taskName: string): string {
    return `task_${taskName.replace(/[^a-zA-Z0-9]+/g, "_")}`;
}
