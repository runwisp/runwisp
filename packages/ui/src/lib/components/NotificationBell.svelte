<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<!--
  Header bell + unread badge + popover shell. The app owns the store and the
  list items (they're domain-specific); it passes them in as `children`.
-->

<script lang="ts">
    import type { Snippet } from "svelte";
    import { Bell } from "@lucide/svelte";
    import Popover from "./Popover.svelte";

    interface Props {
        unread: number;
        /** Badge colour: "danger" when an unread item is an error. */
        tone?: "primary" | "danger";
        open?: boolean;
        /** Panel title; the right side of the header takes `actions`. */
        title?: string;
        actions?: Snippet;
        children: Snippet;
        class?: string;
    }

    let {
        unread,
        tone = "primary",
        open = $bindable(false),
        title = "Notifications",
        actions,
        children,
        class: className = "",
    }: Props = $props();

    let badge = $derived(unread > 99 ? "99+" : String(unread));
</script>

<Popover bind:open placement="bottom-end" mobileSheet class="flex {className}">
    {#snippet trigger()}
        <span
            class="relative flex h-9 w-9 items-center justify-center rounded-[3px] border border-transparent text-on-surface-muted hover:border-outline-hover hover:bg-surface-sunken hover:text-primary"
            title="Notifications"
        >
            <Bell size={18} aria-hidden="true" />
            <span class="sr-only">{title}</span>
            {#if unread > 0}
                <span
                    class="absolute -top-0.5 -right-0.5 inline-flex min-w-[18px] items-center justify-center rounded-full px-1 font-mono text-2xs font-bold tabular-nums {tone ===
                    'danger'
                        ? 'bg-danger-surface text-on-danger'
                        : 'bg-primary text-on-primary'}"
                    aria-label={`${badge} unread`}
                >
                    {badge}
                </span>
            {/if}
        </span>
    {/snippet}

    <div role="dialog" aria-label={title} class="w-96 max-w-[90vw]">
        <div class="mb-2 flex items-center justify-between gap-2">
            <h2 class="font-mono text-sm font-semibold text-on-surface">{title}</h2>
            {#if actions}
                <div class="flex items-center gap-3 text-xs">{@render actions()}</div>
            {/if}
        </div>
        {@render children()}
    </div>
</Popover>
