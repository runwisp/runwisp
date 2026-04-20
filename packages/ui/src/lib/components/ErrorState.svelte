<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Snippet, Component } from "svelte";
    import { TriangleAlert, RefreshCw, WifiOff } from "@lucide/svelte";
    import Button from "./Button.svelte";

    interface Props {
        /** Error message to display */
        message: string;
        /** Title override */
        title?: string;
        /** Icon override */
        icon?: Component;
        /** Callback when the retry button is clicked */
        onRetry?: () => void;
        /** Whether a retry is in progress */
        retrying?: boolean;
        /** Visual variant */
        variant?: "default" | "disconnected";
        /** Extra actions slot */
        actions?: Snippet;
        class?: string;
    }

    let {
        message,
        title,
        icon,
        onRetry,
        retrying = false,
        variant = "default",
        actions,
        class: className = "",
    }: Props = $props();

    const defaultTitle = $derived(
        title ?? (variant === "disconnected" ? "Connection Lost" : "Something went wrong"),
    );

    const IconComponent = $derived(icon ?? (variant === "disconnected" ? WifiOff : TriangleAlert));

    const iconBg = $derived(variant === "disconnected" ? "bg-danger-soft" : "bg-warning-soft");
    const iconColor = $derived(
        variant === "disconnected" ? "text-danger-surface" : "text-warning-surface",
    );
</script>

<div
    class="flex flex-col items-center justify-center gap-4 rounded-xl border border-outline bg-surface-raised px-6 py-12 text-center {className}"
    role="alert"
>
    <div class="rounded-full {iconBg} p-3">
        <IconComponent class="h-8 w-8 {iconColor}" />
    </div>

    <div class="space-y-1">
        <h3 class="text-lg font-semibold text-on-surface">{defaultTitle}</h3>
        <p class="max-w-sm text-sm text-on-surface-muted">{message}</p>
    </div>

    {#if onRetry}
        <Button variant="secondary" onclick={onRetry} loading={retrying}>
            {#snippet icon()}<RefreshCw size={16} />{/snippet}
            Retry
        </Button>
    {/if}

    {#if actions}
        {@render actions()}
    {/if}
</div>
