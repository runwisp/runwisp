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
            label: "Online",
            title: "Connected to the runner API",
            container: "bg-success-soft text-success-soft-text border-success-soft-border",
            dot: "bg-success-surface",
            ping: null,
        },
        connecting: {
            label: "Connecting",
            title: "Attempting to reach the runner API",
            container: "bg-warning-soft text-warning-soft-text border-warning-soft-border",
            dot: "bg-warning-surface",
            ping: "bg-warning-surface",
        },
        disconnected: {
            label: "Offline",
            title: "Click to retry connecting to the runner API",
            container:
                "bg-danger-soft text-danger-soft-text border-danger-soft-border hover:bg-danger-soft/80",
            dot: "bg-danger-surface",
            ping: null,
        },
    };

    let status = $derived(connectionStore.status);
    let theme = $derived(THEMES[status]);
</script>

{#snippet body()}
    <span class="relative flex h-1.5 w-1.5 shrink-0">
        {#if theme.ping}
            <span
                class="absolute inline-flex h-full w-full animate-ping rounded-full opacity-75 {theme.ping}"
            ></span>
        {/if}
        <span class="relative inline-flex h-1.5 w-1.5 rounded-full {theme.dot}"></span>
    </span>
    {#if status !== "connected"}
        <span class="hidden sm:inline">{theme.label}</span>
    {/if}
{/snippet}

{#if status === "connected"}
    <span
        class="duration-normal inline-flex items-center gap-1.5 rounded-full border px-2 py-1 text-xs font-medium transition-colors {theme.container}"
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
        class="duration-normal inline-flex items-center gap-1.5 rounded-full border px-2 py-1 text-xs font-medium transition-colors disabled:cursor-progress {theme.container}"
    >
        {@render body()}
    </button>
{/if}
