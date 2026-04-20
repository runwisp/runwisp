<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    interface Props {
        keys: string | string[];
        class?: string;
    }

    let { keys, class: className = "" }: Props = $props();

    const keyList = $derived(Array.isArray(keys) ? keys : [keys]);

    const kbdClasses = `
        inline-flex items-center justify-center
        min-w-[1.5em] px-1.5 py-0.5
        text-xs font-mono font-medium
        bg-surface-sunken text-on-surface-muted
        border border-outline rounded-md
        shadow-sm
    `;
</script>

<span class="inline-flex items-center gap-1 {className}">
    {#each keyList as key, i (key)}
        {#if i > 0}
            <span class="text-xs text-on-surface-faint select-none">+</span>
        {/if}
        <kbd class={kbdClasses}>{key}</kbd>
    {/each}
</span>
