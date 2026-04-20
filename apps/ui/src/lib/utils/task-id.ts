// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

export function toTaskPageId(taskName: string): string {
    return `task_${taskName.replace(/[^a-zA-Z0-9]+/g, "_")}`;
}
