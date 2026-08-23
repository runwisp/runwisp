<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import { Bell } from "@lucide/svelte";
    import { notificationStore } from "$lib/stores";
    import NotificationsPopover from "./NotificationsPopover.svelte";
    import { hasUnreadError } from "./notification-bell.js";

    let open = $state(false);

    let unread = $derived(notificationStore.unread);
    let hasError = $derived(hasUnreadError(notificationStore.items));

    function toggle(): void {
        open = !open;
    }

    function close(): void {
        open = false;
    }

    function onKeydown(e: KeyboardEvent): void {
        if (e.key === "Escape" && open) close();
    }
</script>

<svelte:window onkeydown={onKeydown} />

<div class="relative">
    <button
        type="button"
        class="relative flex h-9 w-9 items-center justify-center rounded-[3px] border border-transparent text-on-surface-muted hover:border-outline-hover hover:bg-surface-sunken hover:text-primary"
        aria-label="Notifications"
        aria-expanded={open}
        onclick={toggle}
    >
        <Bell size={18} />
        {#if unread > 0}
            <span
                class="absolute -top-0.5 -right-0.5 inline-flex min-w-[18px] items-center justify-center rounded-full px-1 font-mono text-2xs font-bold text-on-primary tabular-nums {hasError
                    ? 'bg-danger-surface'
                    : 'bg-primary'}"
                aria-label={`${unread.toString()} unread notifications`}
            >
                {unread > 99 ? "99+" : unread}
            </span>
        {/if}
    </button>

    {#if open}
        <NotificationsPopover onClose={close} />
    {/if}
</div>
