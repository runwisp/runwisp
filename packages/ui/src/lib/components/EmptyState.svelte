<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Snippet } from "svelte";
    import type { Component } from "svelte";
    import { Inbox } from "@lucide/svelte";

    interface Props {
        title?: string;
        description?: string;
        icon?: Component;
        iconSize?: number;
        actions?: Snippet;
        class?: string;
    }

    let {
        title = "No data",
        description,
        icon = Inbox,
        iconSize = 48,
        actions,
        class: className = "",
    }: Props = $props();

    const Icon = $derived(icon);
</script>

<div class="flex flex-col items-center justify-center px-4 py-12 text-center {className}">
    <div class="mb-4 rounded-full bg-surface-sunken p-4">
        <Icon size={iconSize} class="text-on-surface-faint" />
    </div>

    <h3 class="mb-1 text-lg font-semibold text-on-surface">{title}</h3>

    {#if description}
        <p class="mb-6 max-w-sm text-sm text-on-surface-muted">{description}</p>
    {/if}

    {#if actions}
        <div class="flex items-center gap-3">
            {@render actions()}
        </div>
    {/if}
</div>
