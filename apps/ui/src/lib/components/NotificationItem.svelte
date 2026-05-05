<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { phrase } from "$lib/utils/notification-rhythm";
    import NotificationSparkline from "./NotificationSparkline.svelte";
    import type { Notification } from "$lib/stores/notifications.svelte";

    interface Props {
        notification: Notification;
        compact?: boolean;
    }

    let { notification, compact = false }: Props = $props();

    let rhythm = $derived(
        phrase({
            count: notification.count,
            createdAt: notification.created_at,
            lastOccurredAt: notification.last_occurred_at,
            occurrences: notification.occurrences,
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
</script>

<article
    class="flex gap-3 rounded-lg border border-mist-100 bg-white p-3 transition-colors hover:bg-mist-50"
    aria-label={notification.title || notification.kind}
>
    <span class="mt-1.5 inline-block h-2 w-2 shrink-0 rounded-full {dotClass}" aria-hidden="true"
    ></span>

    <div class="min-w-0 flex-1 space-y-1">
        <div class="flex items-baseline justify-between gap-2">
            <h3 class="truncate text-sm font-semibold text-mist-900">
                {notification.title || notification.kind}
            </h3>
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
            <NotificationSparkline occurrences={notification.occurrences} class="text-mist-500" />
        </div>
    </div>
</article>
