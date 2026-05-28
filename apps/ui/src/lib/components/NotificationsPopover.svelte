<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { resolve } from "$app/paths";
    import Card from "@runwisp/ui/components/Card.svelte";
    import { notificationStore } from "$lib/stores";
    import NotificationItem from "./NotificationItem.svelte";

    interface Props {
        onClose: () => void;
    }

    let { onClose }: Props = $props();

    const POPOVER_LIMIT = 10;
    let items = $derived(notificationStore.items.slice(0, POPOVER_LIMIT));
    let extra = $derived(Math.max(0, notificationStore.items.length - POPOVER_LIMIT));

    async function markAllRead(): Promise<void> {
        await notificationStore.markAllRead();
    }
</script>

<div role="dialog" aria-label="Notifications" class="absolute top-12 right-0 z-40 w-96">
    <Card padding="none" shadow="xl">
        {#snippet header()}
            <div class="flex items-center justify-between">
                <h2 class="text-sm font-semibold text-on-surface">Notifications</h2>
                <div class="flex items-center gap-3 text-xs">
                    {#if notificationStore.unread > 0}
                        <button
                            type="button"
                            class="text-primary-soft-text hover:text-primary-soft-text"
                            onclick={() => void markAllRead()}>Mark all read</button
                        >
                    {/if}
                    <a
                        href={resolve("/notifications")}
                        class="text-on-surface-muted hover:text-on-surface"
                        onclick={onClose}>View all</a
                    >
                </div>
            </div>
        {/snippet}

        <div class="max-h-96 space-y-2 overflow-y-auto p-2">
            {#if items.length === 0}
                <p class="px-2 py-6 text-center text-xs text-on-surface-faint">
                    No notifications yet.
                </p>
            {:else}
                {#each items as item (item.id)}
                    <NotificationItem notification={item} compact onclick={onClose} />
                {/each}
                {#if extra > 0}
                    <a
                        href={resolve("/notifications")}
                        onclick={onClose}
                        class="block rounded-md px-2 py-2 text-center text-xs text-on-surface-muted hover:bg-surface-sunken hover:text-on-surface"
                        >+{extra} more — View all</a
                    >
                {/if}
            {/if}
        </div>
    </Card>
</div>
