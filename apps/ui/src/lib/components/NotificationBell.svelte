<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { Bell } from "@lucide/svelte";
    import { notificationStore } from "$lib/stores";
    import NotificationsPopover from "./NotificationsPopover.svelte";

    let open = $state(false);

    let unread = $derived(notificationStore.unread);
    let hasError = $derived(
        notificationStore.items.slice(0, 5).some((n) => n.severity === "error" && unread > 0),
    );

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
        class="relative flex h-9 w-9 items-center justify-center rounded-lg text-mist-500 transition-colors hover:bg-mist-100 hover:text-mist-900"
        aria-label="Notifications"
        aria-expanded={open}
        onclick={toggle}
    >
        <Bell size={18} />
        {#if unread > 0}
            <span
                class="absolute -top-0.5 -right-0.5 inline-flex min-w-[18px] items-center justify-center rounded-full px-1 text-[10px] font-bold text-white {hasError
                    ? 'bg-danger-500'
                    : 'bg-wisp-600'}"
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
