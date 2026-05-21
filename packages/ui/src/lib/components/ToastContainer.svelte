<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    /**
     * Toast notification component
     * Displays toast notifications for user feedback
     */
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
                return "border-success-200 text-success-700";
            case "error":
                return "border-danger-200 text-danger-700";
            case "warning":
                return "border-warning-200 text-warning-700";
            case "info":
                return "border-wisp-200 text-wisp-700";
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

<!-- Toast Container -->
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
            class="pointer-events-auto isolate flex max-w-md min-w-[320px] items-start gap-3 rounded-xl border bg-surface-overlay/95 px-4 py-3 shadow-lg ring-1 ring-on-surface/5 backdrop-blur-md {getColorClasses(
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
                    class="flex-shrink-0 rounded-md px-2 py-1 text-xs font-semibold text-on-surface underline-offset-2 transition-colors hover:bg-surface-sunken hover:underline"
                >
                    {action.label}
                </button>
            {/if}
            <button
                onclick={() => toast.remove(toastItem.id)}
                class="flex-shrink-0 rounded-lg p-1 text-on-surface-muted transition-colors hover:bg-surface-sunken hover:text-on-surface"
                aria-label="Close notification"
            >
                <X class="h-4 w-4" aria-hidden="true" />
            </button>
        </div>
    {/each}
</div>
