<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { Bell } from "@lucide/svelte";
    import EmptyState from "@runwisp/ui/components/EmptyState.svelte";
    import { notificationStore } from "$lib/stores";
    import NotificationItem from "$lib/components/NotificationItem.svelte";

    let items = $derived(notificationStore.items);
    let unread = $derived(notificationStore.unread);

    async function loadMore(): Promise<void> {
        await notificationStore.loadMore();
    }

    async function markAllRead(): Promise<void> {
        await notificationStore.markAllRead();
    }
</script>

<svelte:head>
    <title>Notifications · RunWisp</title>
</svelte:head>

<div class="mx-auto max-w-3xl space-y-4">
    <header class="flex items-center justify-between">
        <div>
            <h1 class="text-2xl font-bold text-mist-900">Notifications</h1>
            <p class="text-sm text-mist-500">
                Recent notifications across all tasks
                {#if unread > 0}· <span class="font-medium text-wisp-700">{unread} unread</span
                    >{/if}
            </p>
        </div>
        {#if unread > 0}
            <button
                type="button"
                class="rounded-md bg-wisp-50 px-3 py-1.5 text-sm font-medium text-wisp-700 hover:bg-wisp-100"
                onclick={() => void markAllRead()}>Mark all read</button
            >
        {/if}
    </header>

    {#if items.length === 0}
        <div class="rounded-xl border border-dashed border-outline bg-surface-raised">
            <EmptyState
                title="No notifications yet"
                description="Failed runs and notifier delivery problems will appear here."
                icon={Bell}
            />
        </div>
    {:else}
        <div class="space-y-2">
            {#each items as item (item.id)}
                <NotificationItem notification={item} />
            {/each}
        </div>

        {#if notificationStore.hasMore}
            <div class="flex justify-center pt-4">
                <button
                    type="button"
                    class="rounded-md border border-mist-200 bg-surface-raised px-4 py-1.5 text-sm font-medium text-mist-700 hover:bg-mist-50"
                    onclick={() => void loadMore()}>Load more</button
                >
            </div>
        {/if}
    {/if}
</div>
