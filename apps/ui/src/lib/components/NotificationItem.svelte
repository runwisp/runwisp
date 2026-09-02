<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import { resolve } from "$app/paths";
    import { browser } from "$app/environment";
    import { formatShortId, TickingNow } from "@runwisp/ui";
    import { phrase } from "$lib/utils/notification-rhythm";
    import NotificationSparkline from "./NotificationSparkline.svelte";
    import { notificationActionFor } from "./notification-actions";
    import type { Notification } from "$lib/stores/notifications.svelte";

    interface Props {
        notification: Notification;
        compact?: boolean;
        onclick?: () => void;
    }

    let { notification, compact = false, onclick }: Props = $props();

    // Tick once every 30s so relative-time labels and the sparkline window
    // advance without waiting for an SSE event.
    const ticker = new TickingNow();
    $effect(() => {
        if (!browser) return;
        return ticker.start();
    });
    let now = $derived(ticker.now);

    let rhythm = $derived(
        phrase({
            count: notification.count,
            createdAt: notification.createdAt,
            lastOccurredAt: notification.lastOccurredAt,
            occurrences: notification.occurrences,
            now,
        }),
    );

    // Inline action for this kind (e.g. "Update now" on update.available), or
    // undefined for kinds without one. Action-bearing kinds are daemon-level
    // (no taskName), so they render in the <article> branch below — never nested
    // inside the task/run <a> link.
    let Action = $derived(notificationActionFor(notification.kind));

    let dotClass = $derived.by(() => {
        switch (notification.severity) {
            case "error":
                return "bg-danger-surface";
            case "warn":
                return "bg-warning-surface";
            default:
                return "bg-info";
        }
    });
</script>

{#snippet body()}
    <span class="mt-1.5 inline-block h-2 w-2 shrink-0 rounded-full {dotClass}" aria-hidden="true"
    ></span>

    <div class="min-w-0 flex-1 space-y-1">
        <div class="flex items-baseline justify-between gap-2">
            <h3 class="truncate text-sm font-semibold text-on-surface hover:text-primary-soft-text">
                {notification.title || notification.kind}
            </h3>
            <span class="shrink-0 text-2xs text-on-surface-faint">{rhythm}</span>
        </div>

        {#if !compact && notification.body}
            <p class="line-clamp-2 text-xs text-on-surface-muted">{notification.body}</p>
        {/if}

        {#if Action}
            <div class="pt-1">
                <Action {notification} />
            </div>
        {/if}

        {#if notification.runId}
            <span
                class="inline-flex items-center gap-1 rounded-[3px] border border-primary-soft-border bg-primary-soft px-1.5 py-0.5 font-mono text-2xs font-medium text-primary-soft-text"
            >
                View run #{formatShortId(notification.runId)} <span aria-hidden="true">→</span>
            </span>
        {/if}

        <div class="flex items-center justify-between gap-2 text-2xs text-on-surface-faint">
            {#if notification.taskName}
                <span class="truncate font-mono">{notification.taskName}</span>
            {:else}
                <span></span>
            {/if}
            <NotificationSparkline
                occurrences={notification.occurrences}
                {now}
                class="text-on-surface-muted"
            />
        </div>
    </div>
{/snippet}

{#if notification.taskName}
    <a
        href={notification.runId
            ? resolve(
                  `/tasks/${encodeURIComponent(notification.taskName)}/${encodeURIComponent(notification.runId)}`,
              )
            : resolve(`/tasks/${encodeURIComponent(notification.taskName)}`)}
        {onclick}
        data-testid="notification-item"
        class="flex gap-3 rounded-[4px] border border-outline-faint bg-surface-raised p-3 no-underline hover:border-outline-hover hover:bg-surface-sunken"
        aria-label={notification.title || notification.kind}
    >
        {@render body()}
    </a>
{:else}
    <article
        data-testid="notification-item"
        class="flex gap-3 rounded-[4px] border border-outline-faint bg-surface-raised p-3 hover:border-outline-hover hover:bg-surface-sunken"
        aria-label={notification.title || notification.kind}
    >
        {@render body()}
    </article>
{/if}
