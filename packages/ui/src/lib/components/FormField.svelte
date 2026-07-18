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
        // The id of the control this field labels. Pass the SAME id to the wrapped
        // input/select so the <label for> actually points at it — otherwise the
        // label is dead (clicking it does nothing; screen readers can't associate
        // it). Falls back to a generated id when the field has no focusable target.
        id?: string;
    }

    let {
        label,
        hint,
        error,
        description,
        required = false,
        children,
        class: className = "",
        id,
    }: Props = $props();

    const generatedId = `field-${Math.random().toString(36).slice(2, 10)}`;
    const fieldId = $derived(id ?? generatedId);
</script>

<div class="space-y-1.5 {className}">
    {#if label}
        <label for={fieldId} class="block text-sm font-medium text-on-surface-muted">
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
