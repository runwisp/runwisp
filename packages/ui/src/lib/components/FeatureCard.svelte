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
    class="overflow-hidden rounded-xl border border-outline bg-surface-raised p-6 shadow-sm transition-colors hover:border-outline-hover {className}"
>
    <div class="flex flex-col gap-3">
        {#if Icon}
            <span
                class="inline-flex h-10 w-10 items-center justify-center rounded-lg bg-primary-soft text-primary-soft-text"
            >
                <Icon size={20} />
            </span>
        {/if}
        <h3 class="text-lg font-semibold text-on-surface">{title}</h3>
        {#if children}
            <div class="text-sm leading-relaxed text-on-surface-muted">
                {@render children()}
            </div>
        {/if}
    </div>
</article>
