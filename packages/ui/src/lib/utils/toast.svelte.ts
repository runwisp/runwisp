// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { generateUlid } from "@runwisp/common";

export type ToastType = "success" | "error" | "warning" | "info";

export interface Toast {
    id: string;
    type: ToastType;
    message: string;
    duration?: number;
}

const DEFAULT_DURATION = 5000;

class ToastStore {
    items = $state<Toast[]>([]);

    add(type: ToastType, message: string, duration = DEFAULT_DURATION): string {
        const id = generateUlid();
        const t: Toast = { id, type, message, duration };

        this.items = [...this.items, t];

        if (duration > 0) {
            setTimeout(() => {
                this.remove(id);
            }, duration);
        }

        return id;
    }

    remove(id: string) {
        this.items = this.items.filter((t) => t.id !== id);
    }

    success(message: string, duration?: number): string {
        return this.add("success", message, duration);
    }

    error(message: string, duration?: number): string {
        return this.add("error", message, duration);
    }

    warning(message: string, duration?: number): string {
        return this.add("warning", message, duration);
    }

    info(message: string, duration?: number): string {
        return this.add("info", message, duration);
    }

    clear() {
        this.items = [];
    }
}

export const toast = new ToastStore();
