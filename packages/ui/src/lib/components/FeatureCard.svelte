<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Component, Snippet } from "svelte";

    interface Props {
        title: string;
        icon?: Component;
        children?: Snippet;
        class?: string;
    }

    let { title, icon: Icon, children, class: className = "" }: Props = $props();
</script>

<article
    class="overflow-hidden rounded-[4px] border border-outline bg-surface-raised p-6 hover:border-outline-hover {className}"
>
    <div class="flex flex-col gap-3">
        {#if Icon}
            <span
                class="inline-flex h-10 w-10 items-center justify-center rounded-[3px] border border-primary-soft-border bg-primary-soft text-primary-soft-text"
            >
                <Icon size={20} />
            </span>
        {/if}
        <h3 class="font-mono text-base text-primary">{title}</h3>
        {#if children}
            <div class="font-sans text-sm leading-relaxed text-on-surface-muted">
                {@render children()}
            </div>
        {/if}
    </div>
</article>
