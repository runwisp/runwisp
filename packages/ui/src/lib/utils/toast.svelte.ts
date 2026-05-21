// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { generateUlid } from "@runwisp/common";

export type ToastType = "success" | "error" | "warning" | "info";

export interface ToastAction {
    label: string;
    onClick: () => void;
}

export interface Toast {
    id: string;
    type: ToastType;
    message: string;
    duration?: number;
    action?: ToastAction;
}

export interface ToastOptions {
    duration?: number;
    action?: ToastAction;
}

const DEFAULT_DURATION = 5000;

class ToastStore {
    items = $state<Toast[]>([]);
    private readonly timers = new Map<string, ReturnType<typeof setTimeout>>();

    add(type: ToastType, message: string, opts: ToastOptions = {}): string {
        const duration = opts.duration ?? DEFAULT_DURATION;
        const existing = this.items.find((t) => t.type === type && t.message === message);
        if (existing) {
            this.refreshTimer(existing.id, duration);
            return existing.id;
        }

        const id = generateUlid();
        const next: Toast = { id, type, message, duration };
        if (opts.action) next.action = opts.action;
        this.items = [...this.items, next];
        if (duration > 0) {
            this.scheduleRemoval(id, duration);
        }
        return id;
    }

    private scheduleRemoval(id: string, duration: number) {
        this.timers.set(
            id,
            setTimeout(() => {
                this.remove(id);
            }, duration),
        );
    }

    private refreshTimer(id: string, duration: number) {
        const existing = this.timers.get(id);
        if (existing) clearTimeout(existing);
        if (duration > 0) {
            this.scheduleRemoval(id, duration);
        }
    }

    remove(id: string) {
        const timer = this.timers.get(id);
        if (timer) {
            clearTimeout(timer);
            this.timers.delete(id);
        }
        this.items = this.items.filter((t) => t.id !== id);
    }

    success(message: string, opts?: ToastOptions): string {
        return this.add("success", message, opts);
    }

    error(message: string, opts?: ToastOptions): string {
        return this.add("error", message, opts);
    }

    warning(message: string, opts?: ToastOptions): string {
        return this.add("warning", message, opts);
    }

    info(message: string, opts?: ToastOptions): string {
        return this.add("info", message, opts);
    }

    clear() {
        for (const timer of this.timers.values()) {
            clearTimeout(timer);
        }
        this.timers.clear();
        this.items = [];
    }
}

export const toast = new ToastStore();
