<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { formatDuration } from "@runwisp/ui";
    import { connectionStore } from "$lib/stores";

    let status = $derived(connectionStore.status);
    let now = $derived(connectionStore.now);
    let disconnectedSince = $derived(connectionStore.disconnectedSince);

    let subtitle = $derived.by(() => {
        if (status === "connected") return "indev";
        if (status === "connecting") return "Reconnecting…";
        if (disconnectedSince !== null) {
            return "Down for " + formatDuration(now - disconnectedSince);
        }
        return "Not reachable";
    });

    let label = $derived.by(() => {
        if (status === "connected") return "Connected";
        if (status === "connecting") return "Connecting";
        return "Offline";
    });

    async function handleClick() {
        if (status === "connected") return;
        await connectionStore.retryNow();
    }
</script>

{#if status === "connected"}
    <div class="border-t border-mist-100 bg-mist-50/50 p-4" title="Connected to the runner API">
        <div class="flex items-center gap-3">
            <div class="relative flex h-2 w-2">
                <span
                    class="absolute inline-flex h-full w-full animate-ping rounded-full bg-emerald-400 opacity-75"
                ></span>
                <span class="relative inline-flex h-2 w-2 rounded-full bg-emerald-500"></span>
            </div>
            <div class="flex flex-col">
                <span class="text-xs font-medium text-mist-700">{label}</span>
                <span class="text-[10px] text-mist-500">{subtitle}</span>
            </div>
        </div>
    </div>
{:else}
    <button
        type="button"
        onclick={handleClick}
        disabled={status === "connecting"}
        class="group flex w-full items-center gap-3 border-t p-4 text-left transition-colors disabled:cursor-progress
            {status === 'disconnected'
            ? 'border-danger-200 bg-danger-50/70 hover:bg-danger-50'
            : 'border-warning-200 bg-warning-50/70'}"
        title={status === "disconnected"
            ? "Click to retry connecting to the runner API"
            : "Attempting to reach the runner API"}
    >
        <div class="relative flex h-2 w-2 shrink-0">
            {#if status === "connecting"}
                <span
                    class="absolute inline-flex h-full w-full animate-ping rounded-full bg-warning-400 opacity-75"
                ></span>
                <span class="relative inline-flex h-2 w-2 rounded-full bg-warning-500"></span>
            {:else}
                <span class="relative inline-flex h-2 w-2 rounded-full bg-danger-500"></span>
            {/if}
        </div>
        <div class="flex min-w-0 flex-col">
            <span
                class="text-xs font-medium {status === 'disconnected'
                    ? 'text-danger-700'
                    : 'text-warning-700'}">{label}</span
            >
            <span
                class="truncate text-[10px] {status === 'disconnected'
                    ? 'text-danger-600'
                    : 'text-warning-600'}">{subtitle}</span
            >
        </div>
    </button>
{/if}
