<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { connectionStore, type ConnectionStatus } from "$lib/stores";

    interface Theme {
        label: string;
        title: string;
        container: string;
        dot: string;
        ping: string | null;
    }

    const THEMES: Record<ConnectionStatus, Theme> = {
        connected: {
            label: "Connected",
            title: "Connected to the runner API",
            container: "bg-surface-sunken text-on-surface-muted border-outline",
            dot: "bg-success-surface ring-[3px] ring-success-surface/20",
            ping: null,
        },
        connecting: {
            label: "Connecting",
            title: "Attempting to reach the runner API",
            container: "bg-warning-soft text-warning-soft-text border-warning-soft-border",
            dot: "bg-warning-surface ring-[3px] ring-warning-surface/25",
            ping: "bg-warning-surface",
        },
        disconnected: {
            label: "Offline",
            title: "Click to retry connecting to the runner API",
            container:
                "bg-danger-soft text-danger-soft-text border-danger-soft-border hover:bg-danger-soft/80",
            dot: "bg-danger-surface ring-[3px] ring-danger-surface/25",
            ping: "bg-danger-surface",
        },
    };

    let status = $derived(connectionStore.status);
    let theme = $derived(THEMES[status]);
</script>

{#snippet body()}
    <span class="relative flex h-[7px] w-[7px] shrink-0">
        {#if theme.ping}
            <span
                class="absolute inline-flex h-full w-full animate-ping rounded-full opacity-75 {theme.ping}"
            ></span>
        {/if}
        <span class="relative inline-flex h-[7px] w-[7px] rounded-full {theme.dot}"></span>
    </span>
    <span class="hidden sm:inline">{theme.label}</span>
{/snippet}

{#if status === "connected"}
    <span
        class="duration-normal inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs font-medium transition-colors {theme.container}"
        title={theme.title}
        aria-label={theme.label}
    >
        {@render body()}
    </span>
{:else}
    <button
        type="button"
        onclick={connectionStore.retryNow}
        disabled={status === "connecting"}
        title={theme.title}
        aria-label={theme.label}
        class="duration-normal inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs font-medium transition-colors disabled:cursor-progress {theme.container}"
    >
        {@render body()}
    </button>
{/if}
