<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Snippet } from "svelte";
    import { ChevronRight, House } from "@lucide/svelte";

    interface BreadcrumbItem {
        label: string;
        href?: string;
        icon?: typeof House;
    }

    interface Props {
        items: BreadcrumbItem[];
        separator?: Snippet;
        class?: string;
    }

    let { items, separator, class: className = "" }: Props = $props();
</script>

<nav aria-label="Breadcrumb" class={className}>
    <ol class="flex items-center gap-1.5 text-sm">
        {#each items as item, idx (idx)}
            <li class="flex items-center gap-1.5">
                {#if idx > 0}
                    {#if separator}
                        {@render separator()}
                    {:else}
                        <ChevronRight size={14} class="text-on-surface-faint" />
                    {/if}
                {/if}

                {#if item.href && idx < items.length - 1}
                    <a
                        href={item.href}
                        class="flex items-center gap-1.5 text-on-surface-muted transition-colors hover:text-on-surface"
                    >
                        {#if item.icon}
                            {@const Icon = item.icon}
                            <Icon size={14} />
                        {/if}
                        <span>{item.label}</span>
                    </a>
                {:else}
                    <span class="flex items-center gap-1.5 font-medium text-on-surface">
                        {#if item.icon}
                            {@const Icon = item.icon}
                            <Icon size={14} />
                        {/if}
                        <span>{item.label}</span>
                    </span>
                {/if}
            </li>
        {/each}
    </ol>
</nav>
