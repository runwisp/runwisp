<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { resolve } from "$app/paths";
    import { browser } from "$app/environment";
    import { formatShortId } from "@runwisp/ui";
    import { phrase } from "$lib/utils/notification-rhythm";
    import { TickingNow } from "$lib/utils/ticking-now.svelte";
    import NotificationSparkline from "./NotificationSparkline.svelte";
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
            createdAt: notification.created_at,
            lastOccurredAt: notification.last_occurred_at,
            occurrences: notification.occurrences,
            now,
        }),
    );

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

        {#if notification.run_id}
            <span
                class="inline-flex items-center gap-1 rounded-md bg-primary-soft px-1.5 py-0.5 font-mono text-2xs font-medium text-primary-soft-text"
            >
                View run #{formatShortId(notification.run_id)} <span aria-hidden="true">→</span>
            </span>
        {/if}

        <div class="flex items-center justify-between gap-2 text-2xs text-on-surface-faint">
            {#if notification.task_name}
                <span class="truncate">{notification.task_name}</span>
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

{#if notification.task_name}
    <a
        href={notification.run_id
            ? resolve(
                  `/tasks/${encodeURIComponent(notification.task_name)}/${encodeURIComponent(notification.run_id)}`,
              )
            : resolve(`/tasks/${encodeURIComponent(notification.task_name)}`)}
        {onclick}
        data-testid="notification-item"
        class="flex gap-3 rounded-lg border border-outline-faint bg-surface-raised p-3 no-underline transition-colors hover:bg-surface-sunken"
        aria-label={notification.title || notification.kind}
    >
        {@render body()}
    </a>
{:else}
    <article
        data-testid="notification-item"
        class="flex gap-3 rounded-lg border border-outline-faint bg-surface-raised p-3 transition-colors hover:bg-surface-sunken"
        aria-label={notification.title || notification.kind}
    >
        {@render body()}
    </article>
{/if}
