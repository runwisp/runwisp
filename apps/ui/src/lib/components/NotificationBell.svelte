<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import { resolve } from "$app/paths";
    import { NotificationBell } from "@runwisp/ui";
    import { notificationStore } from "$lib/stores";
    import NotificationItem from "./NotificationItem.svelte";
    import { hasUnreadError } from "./notification-bell.js";

    const POPOVER_LIMIT = 10;

    let open = $state(false);
    let items = $derived(notificationStore.items.slice(0, POPOVER_LIMIT));
    let extra = $derived(Math.max(0, notificationStore.items.length - POPOVER_LIMIT));

    function close(): void {
        open = false;
    }
</script>

<NotificationBell
    bind:open
    unread={notificationStore.unread}
    tone={hasUnreadError(notificationStore.items) ? "danger" : "primary"}
>
    {#snippet actions()}
        {#if notificationStore.unread > 0}
            <button
                type="button"
                class="text-primary-soft-text hover:underline"
                onclick={() => void notificationStore.markAllRead()}>Mark all read</button
            >
        {/if}
        <a
            href={resolve("/notifications")}
            class="text-on-surface-muted hover:text-on-surface"
            onclick={close}>View all</a
        >
    {/snippet}

    <div class="-mx-2 max-h-96 space-y-2 overflow-y-auto px-2">
        {#if items.length === 0}
            <p class="px-2 py-6 text-center text-xs text-on-surface-faint">No notifications yet.</p>
        {:else}
            {#each items as item (item.id)}
                <NotificationItem notification={item} compact onclick={close} />
            {/each}
            {#if extra > 0}
                <a
                    href={resolve("/notifications")}
                    onclick={close}
                    class="block rounded-[3px] px-2 py-2 text-center font-mono text-xs text-on-surface-muted hover:bg-surface-sunken hover:text-primary"
                    >+{extra} more — View all</a
                >
            {/if}
        {/if}
    </div>
</NotificationBell>
