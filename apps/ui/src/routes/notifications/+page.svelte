<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import { Bell } from "@lucide/svelte";
    import { EmptyState, ErrorState, Link, PageHeader, Skeleton } from "@runwisp/ui";
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

{#snippet markAllReadButton()}
    <button
        type="button"
        class="rounded-[3px] border border-primary-soft-border bg-primary-soft px-3 py-1.5 font-mono text-sm font-medium text-primary-soft-text hover:border-outline-hover"
        onclick={() => void markAllRead()}>Mark all read</button
    >
{/snippet}

<svelte:head>
    <title>Notifications · RunWisp</title>
</svelte:head>

<div class="mx-auto max-w-3xl space-y-4">
    <PageHeader title="Notifications" actions={unread > 0 ? markAllReadButton : undefined}>
        {#snippet subtitle()}
            Recent notifications across all tasks
            {#if unread > 0}· <span
                    class="font-mono font-medium text-primary-soft-text tabular-nums"
                    >{unread} unread</span
                >{/if}
        {/snippet}
    </PageHeader>

    {#if notificationStore.loadFailed}
        <ErrorState
            message="Couldn't load notifications."
            onRetry={() => void notificationStore.init()}
        />
    {:else if !notificationStore.loaded}
        <Skeleton rows={4} header={false} />
    {:else if items.length === 0}
        <div class="rounded-[4px] border border-dashed border-outline bg-surface-raised">
            <EmptyState
                title="No notifications yet"
                description="Failed runs and notifier delivery problems will appear here. Configure Slack or Telegram notifiers in your runwisp.toml."
                icon={Bell}
            >
                {#snippet actions()}
                    <Link
                        href="https://docs.runwisp.com/notifications/"
                        external
                        variant="primary"
                        class="text-sm font-medium"
                    >
                        Notification docs →
                    </Link>
                {/snippet}
            </EmptyState>
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
                    class="rounded-[3px] border border-outline bg-surface-raised px-4 py-1.5 font-mono text-sm font-medium text-on-surface-muted hover:border-outline-hover hover:text-primary"
                    onclick={() => void loadMore()}>Load more</button
                >
            </div>
        {/if}
    {/if}
</div>
