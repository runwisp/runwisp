<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Snippet } from "svelte";
    import Heading from "./Heading.svelte";

    interface Props {
        title: string;
        /** Plain text, or a snippet when the line carries tokens (counts, names). */
        subtitle?: string | Snippet;
        actions?: Snippet | undefined;
        class?: string;
    }

    let { title, subtitle, actions, class: className = "" }: Props = $props();
</script>

<div class="flex shrink-0 flex-wrap items-start justify-between gap-3 px-1 {className}">
    <div class="min-w-0">
        <Heading level={1} size="2xl">{title}</Heading>
        {#if typeof subtitle === "function"}
            <p class="mt-0.5 text-sm text-on-surface-muted">{@render subtitle()}</p>
        {:else if subtitle}
            <p class="mt-0.5 text-sm text-on-surface-muted">{subtitle}</p>
        {/if}
    </div>
    {#if actions}
        <div class="flex shrink-0 items-center gap-2">
            {@render actions()}
        </div>
    {/if}
</div>
