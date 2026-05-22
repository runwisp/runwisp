<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Snippet } from "svelte";

    interface Props {
        title: string;
        subtitle?: string;
        actions?: Snippet;
        class?: string;
    }

    let { title, subtitle, actions, class: className = "" }: Props = $props();
</script>

<div class="flex shrink-0 flex-wrap items-start justify-between gap-3 px-1 {className}">
    <div class="min-w-0">
        <h1 class="text-2xl font-bold tracking-tight text-on-surface">{title}</h1>
        {#if subtitle}
            <p class="mt-0.5 text-sm text-on-surface-muted">{subtitle}</p>
        {/if}
    </div>
    {#if actions}
        <div class="flex shrink-0 items-center gap-2">
            {@render actions()}
        </div>
    {/if}
</div>
