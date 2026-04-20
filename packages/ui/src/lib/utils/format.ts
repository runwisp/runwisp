// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { formatDistanceToNow } from "date-fns";

export function formatBytes(bytes: number): string {
    const units: [string, ...string[]] = ["B", "KB", "MB", "GB", "TB", "PB"];
    let value = bytes;
    let unitIndex = 0;
    while (value >= 1024 && unitIndex < units.length - 1) {
        value /= 1024;
        unitIndex++;
    }
    const unit = units[unitIndex] ?? "B";
    const digits = value < 10 && unitIndex > 0 ? 1 : 0;
    return `${new Intl.NumberFormat("en", { maximumFractionDigits: digits }).format(value)} ${unit}`;
}

export function formatRelativeTime(dateStr: string | Date): string {
    return formatDistanceToNow(new Date(dateStr), { addSuffix: true });
}

export function formatRelativeTimeWithAbsolute(dateStr: string | Date): string {
    const date = new Date(dateStr);
    const relative = formatDistanceToNow(date, { addSuffix: true }).replace("about ", "");
    const now = new Date();
    const diffMs = Math.abs(now.getTime() - date.getTime());
    const diffDays = diffMs / (1000 * 60 * 60 * 24);

    let absolute: string;
    if (diffDays < 1) {
        absolute = date.toLocaleTimeString(undefined, {
            hour: "numeric",
            minute: "2-digit",
            hour12: false,
        });
    } else {
        absolute = date.toLocaleDateString(undefined, { month: "short", day: "numeric" });
    }

    return `${relative} (${absolute})`;
}

export function formatDateTime(dateStr: string): string {
    return new Date(dateStr).toLocaleString(undefined, {
        year: "numeric",
        month: "short",
        day: "numeric",
        hour: "numeric",
        minute: "2-digit",
    });
}

export function formatDuration(ms: number): string {
    if (ms < 1000) return String(ms) + "ms";
    const s = Math.floor(ms / 1000);
    if (s < 60) return String(s) + "s";
    const m = Math.floor(s / 60);
    const rem = s % 60;
    if (m < 60) return rem > 0 ? String(m) + "m " + String(rem) + "s" : String(m) + "m";
    const h = Math.floor(m / 60);
    const remM = m % 60;
    return remM > 0 ? String(h) + "h " + String(remM) + "m" : String(h) + "h";
}

export function formatFullDateTime(date: Date | string): string {
    return new Intl.DateTimeFormat(undefined, {
        year: "numeric",
        month: "short",
        day: "2-digit",
        hour: "2-digit",
        minute: "2-digit",
        second: "2-digit",
        hour12: false,
    }).format(new Date(date));
}
