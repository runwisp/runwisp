<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { resolve } from "$app/paths";
    import { browser } from "$app/environment";
    import { phrase } from "$lib/utils/notification-rhythm";
    import NotificationSparkline from "./NotificationSparkline.svelte";
    import type { Notification } from "$lib/stores/notifications.svelte";

    interface Props {
        notification: Notification;
        compact?: boolean;
    }

    let { notification, compact = false }: Props = $props();

    // Tick once every 30s so relative-time labels and the sparkline window
    // advance without waiting for an SSE event.
    let nowMs = $state(Date.now());
    $effect(() => {
        if (!browser) return;
        const t = setInterval(() => {
            nowMs = Date.now();
        }, 30_000);
        return () => clearInterval(t);
    });
    let now = $derived(new Date(nowMs));

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
                return "bg-danger-500";
            case "warn":
                return "bg-warning-500";
            default:
                return "bg-aurora-500";
        }
    });

    let isClickable = $derived(Boolean(notification.run_id && notification.task_name));
</script>

<article
    class="flex gap-3 rounded-lg border border-mist-100 bg-white p-3 transition-colors hover:bg-mist-50"
    aria-label={notification.title || notification.kind}
>
    <span class="mt-1.5 inline-block h-2 w-2 shrink-0 rounded-full {dotClass}" aria-hidden="true"
    ></span>

    <div class="min-w-0 flex-1 space-y-1">
        <div class="flex items-baseline justify-between gap-2">
            {#if isClickable}
                <a
                    href={resolve("/tasks/[id]", { id: notification.task_name })}
                    class="truncate text-sm font-semibold text-mist-900 no-underline hover:text-wisp-700"
                >
                    {notification.title || notification.kind}
                </a>
            {:else}
                <h3 class="truncate text-sm font-semibold text-mist-900">
                    {notification.title || notification.kind}
                </h3>
            {/if}
            <span class="shrink-0 text-2xs text-mist-400">{rhythm}</span>
        </div>

        {#if !compact && notification.body}
            <p class="line-clamp-2 text-xs text-mist-600">{notification.body}</p>
        {/if}

        <div class="flex items-center justify-between gap-2 text-2xs text-mist-400">
            {#if notification.task_name}
                <span class="truncate">{notification.task_name}</span>
            {:else}
                <span></span>
            {/if}
            <NotificationSparkline
                occurrences={notification.occurrences}
                {now}
                class="text-mist-500"
            />
        </div>
    </div>
</article>
