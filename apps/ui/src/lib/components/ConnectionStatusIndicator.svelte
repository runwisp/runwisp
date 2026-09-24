<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import { Globe, MessageSquareHeart, TriangleAlert } from "@lucide/svelte";
    import { formatDuration, Popover, StatusDot, type StatusDotTone } from "@runwisp/ui";
    import {
        connectionStore,
        feedbackStore,
        systemStore,
        type ConnectionStatus,
    } from "$lib/stores";
    import { appEventStream } from "$lib/stores/app-stream.svelte";
    import { stalledCopy } from "$lib/utils/connection-copy";

    interface Theme {
        label: string;
        container: string;
        title: string;
        labelColor: string;
        subtitleColor: string;
        tone: StatusDotTone;
        pulse: boolean;
    }

    const THEMES: Record<ConnectionStatus, Theme> = {
        connected: {
            label: "Connected",
            container: "bg-surface-sunken/50 border-outline-faint",
            title: "Connected to the runner API",
            labelColor: "text-on-surface-muted",
            subtitleColor: "text-on-surface-muted",
            tone: "success",
            pulse: true,
        },
        connecting: {
            label: "Connecting",
            container: "bg-warning-soft/70 border-warning-soft-border",
            title: "Attempting to reach the runner API",
            labelColor: "text-warning-soft-text",
            subtitleColor: "text-warning-soft-text",
            tone: "warning",
            pulse: true,
        },
        disconnected: {
            label: "Offline",
            container: "bg-danger-soft/70 border-danger-soft-border hover:bg-danger-soft",
            title: "Click to retry connecting to the runner API",
            labelColor: "text-danger-soft-text",
            subtitleColor: "text-danger-soft-text",
            tone: "danger",
            pulse: false,
        },
        stalled: {
            label: "Updates paused",
            container: "bg-warning-soft/70 border-warning-soft-border",
            title: "",
            labelColor: "text-warning-soft-text",
            subtitleColor: "text-warning-soft-text",
            tone: "warning",
            pulse: false,
        },
    };

    let status = $derived(connectionStore.status);
    // Stalled copy depends on whether this tab shares one connection across tabs:
    // only the degraded per-tab mode can honestly blame "too many tabs".
    let copy = $derived(stalledCopy(appEventStream.sharing));
    let theme = $derived(
        status === "stalled"
            ? { ...THEMES.stalled, label: copy.label, title: copy.title }
            : THEMES[status],
    );

    let subtitle = $derived.by(() => {
        if (status === "connected") return `v${systemStore.version}`;
        if (status === "connecting") return "Reconnecting…";
        if (status === "stalled") return copy.hint;
        const since = connectionStore.disconnectedSince;
        if (typeof since === "number")
            return "Down for " + formatDuration(connectionStore.now - since);
        return "Not reachable";
    });
</script>

{#snippet body()}
    <StatusDot tone={theme.tone} pulse={theme.pulse} />
    <div class="flex min-w-0 flex-col">
        <span class="font-mono text-xs font-medium {theme.labelColor}">{theme.label}</span>
        <span class="flex items-center gap-1 font-mono text-2xs {theme.subtitleColor}">
            <span class="truncate">{subtitle}</span>
            {#if status === "connected" && systemStore.updateAvailable}
                <Popover placement="top" class="shrink-0 leading-none">
                    {#snippet trigger()}
                        <span
                            class="flex cursor-pointer items-center text-warning-surface hover:text-warning-soft-text"
                            title="A newer runwisp release is available"
                        >
                            <TriangleAlert size={12} />
                            <span class="sr-only">Update available</span>
                        </span>
                    {/snippet}
                    <div class="flex w-56 flex-col gap-1 font-sans text-xs">
                        <span class="flex items-center gap-1.5 font-medium text-on-surface">
                            <TriangleAlert size={14} class="shrink-0 text-warning-surface" />
                            Update available
                        </span>
                        <span class="text-on-surface-muted">
                            <span class="font-mono text-on-surface"
                                >{systemStore.latestVersion}</span
                            >
                            is out, you're on
                            <span class="font-mono">v{systemStore.version}</span>.
                        </span>
                        <a
                            href="https://github.com/runwisp/runwisp/releases"
                            target="_blank"
                            rel="noreferrer"
                            class="mt-1 text-primary hover:underline"
                        >
                            View release notes ↗
                        </a>
                    </div>
                </Popover>
            {/if}
            {#if status === "connected" && systemStore.timezone}
                <span class="shrink-0 text-on-surface-faint">·</span>
                <span
                    title={systemStore.timezoneSource === "system"
                        ? "Detected from the host system; pin [daemon] timezone in runwisp.toml to make it explicit."
                        : "Set in runwisp.toml under [daemon] timezone."}
                    class="flex min-w-0 shrink items-center gap-0.5 truncate"
                >
                    <Globe size={10} class="shrink-0 text-on-surface-faint" />
                    <span class="truncate">{systemStore.timezone}</span>
                </span>
            {/if}
        </span>
        {#if status === "connected" && systemStore.fingerprint}
            <span
                class="truncate font-mono text-2xs text-on-surface-faint"
                title="Instance fingerprint"
            >
                {systemStore.fingerprint}
            </span>
        {/if}
    </div>
{/snippet}

{#if status === "connected" || status === "stalled"}
    <div class="flex items-center gap-3 border-t p-4 {theme.container}" title={theme.title}>
        {@render body()}
        {#if status === "connected" && systemStore.checkUpdates && !feedbackStore.open}
            <button
                type="button"
                title="Send feedback"
                aria-label="Send feedback"
                class="ml-auto shrink-0 rounded-[3px] p-1 text-on-surface-faint hover:bg-surface-sunken hover:text-primary"
                onclick={feedbackStore.show}
            >
                <MessageSquareHeart size={14} />
            </button>
        {/if}
    </div>
{:else}
    <button
        type="button"
        onclick={connectionStore.retryNow}
        disabled={status === "connecting"}
        title={theme.title}
        class="flex w-full items-center gap-3 border-t p-4 text-left disabled:cursor-progress {theme.container}"
    >
        {@render body()}
    </button>
{/if}
