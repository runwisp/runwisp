<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { fly, fade } from "svelte/transition";
    import { CircleCheck, CircleX, Info, TriangleAlert, X } from "@lucide/svelte";
    import { toast, type Toast } from "../utils/toast.svelte.js";

    function getIcon(type: Toast["type"]) {
        switch (type) {
            case "success":
                return CircleCheck;
            case "error":
                return CircleX;
            case "warning":
                return TriangleAlert;
            case "info":
                return Info;
        }
    }

    function getColorClasses(type: Toast["type"]) {
        switch (type) {
            case "success":
                return "border-success-soft-border text-success-soft-text";
            case "error":
                return "border-danger-soft-border text-danger-soft-text";
            case "warning":
                return "border-warning-soft-border text-warning-soft-text";
            case "info":
                return "border-primary-soft-border text-primary-soft-text";
        }
    }

    function getIconColor(type: Toast["type"]) {
        switch (type) {
            case "success":
                return "text-success-surface";
            case "error":
                return "text-danger-surface";
            case "warning":
                return "text-warning-surface";
            case "info":
                return "text-info";
        }
    }
</script>

<div
    class="pointer-events-none fixed bottom-4 left-1/2 z-[100] flex -translate-x-1/2 flex-col gap-2"
    role="region"
    aria-label="Notifications"
    aria-live="polite"
>
    {#each toast.items as toastItem (toastItem.id)}
        {@const Icon = getIcon(toastItem.type)}
        <div
            in:fly={{ y: 20, duration: 300 }}
            out:fade={{ duration: 200 }}
            class="pointer-events-auto isolate flex max-w-md min-w-[320px] items-start gap-3 rounded-[4px] border bg-surface-overlay px-4 py-3 shadow-lg {getColorClasses(
                toastItem.type,
            )}"
            role="alert"
        >
            <Icon
                class="mt-0.5 h-5 w-5 flex-shrink-0 {getIconColor(toastItem.type)}"
                aria-hidden="true"
            />
            <p class="flex-1 text-sm font-medium">{toastItem.message}</p>
            {#if toastItem.action}
                {@const action = toastItem.action}
                <button
                    onclick={() => {
                        action.onClick();
                        toast.remove(toastItem.id);
                    }}
                    class="flex-shrink-0 rounded-[3px] px-2 py-1 font-mono text-xs font-semibold text-on-surface underline-offset-2 hover:bg-surface-sunken hover:underline"
                >
                    {action.label}
                </button>
            {/if}
            <button
                onclick={() => toast.remove(toastItem.id)}
                class="flex-shrink-0 rounded-[3px] p-1 text-on-surface-muted hover:bg-surface-sunken hover:text-on-surface"
                aria-label="Close notification"
            >
                <X class="h-4 w-4" aria-hidden="true" />
            </button>
        </div>
    {/each}
</div>
