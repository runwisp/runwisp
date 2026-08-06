<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import { formatDuration } from "@runwisp/ui";
    import { connectionStore, systemStore, type ConnectionStatus } from "$lib/stores";
    import { connectionTheme, type ConnectionThemeBase } from "$lib/utils/connection-theme.svelte";

    interface Theme extends ConnectionThemeBase {
        container: string;
        labelColor: string;
        subtitleColor: string;
        dot: string;
        ping: string | null;
    }

    const THEMES: Record<ConnectionStatus, Theme> = {
        connected: {
            label: "Connected",
            container: "bg-surface-sunken/50 border-outline-faint",
            title: "Connected to the runner API",
            labelColor: "text-on-surface-muted",
            subtitleColor: "text-on-surface-muted",
            dot: "bg-success-surface",
            ping: "bg-success-surface",
        },
        connecting: {
            label: "Connecting",
            container: "bg-warning-soft/70 border-warning-soft-border",
            title: "Attempting to reach the runner API",
            labelColor: "text-warning-soft-text",
            subtitleColor: "text-warning-soft-text",
            dot: "bg-warning-surface",
            ping: "bg-warning-surface",
        },
        disconnected: {
            label: "Offline",
            container: "bg-danger-soft/70 border-danger-soft-border hover:bg-danger-soft",
            title: "Click to retry connecting to the runner API",
            labelColor: "text-danger-soft-text",
            subtitleColor: "text-danger-soft-text",
            dot: "bg-danger-surface",
            ping: null,
        },
        stalled: {
            label: "Updates paused",
            container: "bg-warning-soft/70 border-warning-soft-border",
            title: "",
            labelColor: "text-warning-soft-text",
            subtitleColor: "text-warning-soft-text",
            dot: "bg-warning-surface",
            ping: null,
        },
    };

    const conn = connectionTheme(THEMES);

    let subtitle = $derived.by(() => {
        if (conn.status === "connected") return `v${systemStore.version}`;
        if (conn.status === "connecting") return "Reconnecting…";
        if (conn.status === "stalled") return conn.copy.hint;
        const since = connectionStore.disconnectedSince;
        if (typeof since === "number")
            return "Down for " + formatDuration(connectionStore.now - since);
        return "Not reachable";
    });
</script>

{#snippet body()}
    <div class="relative flex h-2 w-2 shrink-0">
        {#if conn.theme.ping}
            <span
                class="absolute inline-flex h-full w-full animate-ping rounded-full opacity-75 {conn
                    .theme.ping}"
            ></span>
        {/if}
        <span class="relative inline-flex h-2 w-2 rounded-full {conn.theme.dot}"></span>
    </div>
    <div class="flex min-w-0 flex-col">
        <span class="font-mono text-xs font-medium {conn.theme.labelColor}">{conn.theme.label}</span
        >
        <span class="truncate font-mono text-2xs {conn.theme.subtitleColor}">{subtitle}</span>
    </div>
{/snippet}

{#if conn.status === "connected" || conn.status === "stalled"}
    <div
        class="flex items-center gap-3 border-t p-4 {conn.theme.container}"
        title={conn.theme.title}
    >
        {@render body()}
    </div>
{:else}
    <button
        type="button"
        onclick={connectionStore.retryNow}
        disabled={conn.status === "connecting"}
        title={conn.theme.title}
        class="flex w-full items-center gap-3 border-t p-4 text-left disabled:cursor-progress {conn
            .theme.container}"
    >
        {@render body()}
    </button>
{/if}
