<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { TriangleAlert, RefreshCw, WifiOff } from "@lucide/svelte";
    import { Button } from "@runwisp/ui";

    interface Props {
        /** Error message to display */
        message: string;
        /** Callback when the retry button is clicked */
        onRetry?: () => void;
        /** Whether a retry is in progress */
        retrying?: boolean;
        /** Visual variant */
        variant?: "default" | "disconnected";
        class?: string;
    }

    let {
        message,
        onRetry,
        retrying = false,
        variant = "default",
        class: className = "",
    }: Props = $props();
</script>

<div
    class="flex flex-col items-center justify-center gap-4 rounded-xl border border-mist-200 bg-white px-6 py-12 text-center {className}"
    role="alert"
>
    {#if variant === "disconnected"}
        <div class="rounded-full bg-danger-50 p-3">
            <WifiOff class="h-8 w-8 text-danger-500" />
        </div>
    {:else}
        <div class="rounded-full bg-warning-50 p-3">
            <TriangleAlert class="h-8 w-8 text-warning-500" />
        </div>
    {/if}

    <div class="space-y-1">
        <h3 class="text-lg font-semibold text-mist-900">
            {variant === "disconnected" ? "Connection Lost" : "Something went wrong"}
        </h3>
        <p class="max-w-sm text-sm text-mist-600">{message}</p>
    </div>

    {#if onRetry}
        <Button variant="secondary" onclick={onRetry} loading={retrying}>
            {#snippet icon()}<RefreshCw size={16} />{/snippet}
            Retry
        </Button>
    {/if}
</div>
