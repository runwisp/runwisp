<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { resolve } from "$app/paths";
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

<div
    role="dialog"
    aria-label="Notifications"
    class="absolute top-12 right-0 z-40 w-96 rounded-lg border border-mist-200 bg-white shadow-xl shadow-mist-900/10"
>
    <header class="flex items-center justify-between border-b border-mist-100 px-4 py-2">
        <h2 class="text-sm font-semibold text-mist-900">Notifications</h2>
        <div class="flex items-center gap-3 text-xs">
            {#if notificationStore.unread > 0}
                <button
                    type="button"
                    class="text-wisp-700 hover:text-wisp-800"
                    onclick={() => void markAllRead()}>Mark all read</button
                >
            {/if}
            <a
                href={resolve("/notifications")}
                class="text-mist-500 hover:text-mist-700"
                onclick={onClose}>View all</a
            >
        </div>
    </header>

    <div class="max-h-96 space-y-2 overflow-y-auto p-2">
        {#if items.length === 0}
            <p class="px-2 py-6 text-center text-xs text-mist-400">No notifications yet.</p>
        {:else}
            {#each items as item (item.id)}
                <NotificationItem notification={item} compact onclick={onClose} />
            {/each}
            {#if extra > 0}
                <a
                    href={resolve("/notifications")}
                    onclick={onClose}
                    class="block rounded-md px-2 py-2 text-center text-xs text-mist-500 hover:bg-mist-50 hover:text-mist-700"
                    >+{extra} more — View all</a
                >
            {/if}
        {/if}
    </div>
</div>
