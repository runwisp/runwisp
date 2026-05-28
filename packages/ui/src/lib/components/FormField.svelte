<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Snippet } from "svelte";

    interface Props {
        label?: string;
        hint?: string;
        error?: string;
        description?: string;
        required?: boolean;
        children: Snippet;
        class?: string;
    }

    let {
        label,
        hint,
        error,
        description,
        required = false,
        children,
        class: className = "",
    }: Props = $props();

    const generatedId = `field-${Math.random().toString(36).slice(2, 10)}`;
</script>

<div class="space-y-1.5 {className}">
    {#if label}
        <label for={generatedId} class="block text-sm font-medium text-on-surface-muted">
            {label}
            {#if required}
                <span class="text-danger-soft-text">*</span>
            {/if}
        </label>
    {/if}

    {#if description}
        <p class="text-sm text-on-surface-faint">{description}</p>
    {/if}

    {@render children()}

    {#if error}
        <p
            class="animate-in slide-in-from-top-1 fade-in text-sm text-danger-soft-text duration-200"
        >
            {error}
        </p>
    {:else if hint}
        <p class="text-sm text-on-surface-muted">{hint}</p>
    {/if}
</div>
