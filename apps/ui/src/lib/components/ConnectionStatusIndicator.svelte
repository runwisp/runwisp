<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { formatDuration } from "@runwisp/ui";
    import { connectionStore, systemStore, type ConnectionStatus } from "$lib/stores";

    interface Theme {
        label: string;
        container: string;
        title: string;
        labelColor: string;
        subtitleColor: string;
        dot: string;
        ping: string | null;
    }

    const THEMES: Record<ConnectionStatus, Theme> = {
        connected: {
            label: "Connected",
            container: "bg-mist-50/50 border-mist-100",
            title: "Connected to the runner API",
            labelColor: "text-mist-700",
            subtitleColor: "text-mist-500",
            dot: "bg-emerald-500",
            ping: "bg-emerald-400",
        },
        connecting: {
            label: "Connecting",
            container: "bg-warning-50/70 border-warning-200",
            title: "Attempting to reach the runner API",
            labelColor: "text-warning-700",
            subtitleColor: "text-warning-600",
            dot: "bg-warning-500",
            ping: "bg-warning-400",
        },
        disconnected: {
            label: "Offline",
            container: "bg-danger-50/70 border-danger-200 hover:bg-danger-50",
            title: "Click to retry connecting to the runner API",
            labelColor: "text-danger-700",
            subtitleColor: "text-danger-600",
            dot: "bg-danger-500",
            ping: null,
        },
    };

    let status = $derived(connectionStore.status);
    let theme = $derived(THEMES[status]);

    let subtitle = $derived.by(() => {
        if (status === "connected") return `v${systemStore.version}`;
        if (status === "connecting") return "Reconnecting…";
        const since = connectionStore.disconnectedSince;
        if (typeof since === "number")
            return "Down for " + formatDuration(connectionStore.now - since);
        return "Not reachable";
    });
</script>

{#snippet body()}
    <div class="relative flex h-2 w-2 shrink-0">
        {#if theme.ping}
            <span
                class="absolute inline-flex h-full w-full animate-ping rounded-full opacity-75 {theme.ping}"
            ></span>
        {/if}
        <span class="relative inline-flex h-2 w-2 rounded-full {theme.dot}"></span>
    </div>
    <div class="flex min-w-0 flex-col">
        <span class="text-xs font-medium {theme.labelColor}">{theme.label}</span>
        <span class="truncate text-[10px] {theme.subtitleColor}">{subtitle}</span>
    </div>
{/snippet}

{#if status === "connected"}
    <div class="flex items-center gap-3 border-t p-4 {theme.container}" title={theme.title}>
        {@render body()}
    </div>
{:else}
    <button
        type="button"
        onclick={connectionStore.retryNow}
        disabled={status === "connecting"}
        title={theme.title}
        class="flex w-full items-center gap-3 border-t p-4 text-left transition-colors disabled:cursor-progress {theme.container}"
    >
        {@render body()}
    </button>
{/if}
