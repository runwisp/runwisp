<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Snippet } from "svelte";

    type Orientation = "horizontal" | "vertical";

    interface Props {
        value?: string;
        name: string;
        label?: string;
        error?: string;
        hint?: string;
        orientation?: Orientation;
        children: Snippet;
        class?: string;
    }

    let {
        value = $bindable(""),
        name: _name,
        label,
        error,
        hint,
        orientation = "vertical",
        children,
        class: className = "",
    }: Props = $props();

    const orientationClasses: Record<Orientation, string> = {
        horizontal: "flex flex-row flex-wrap gap-x-6 gap-y-3",
        vertical: "flex flex-col gap-3",
    };
</script>

<fieldset class="space-y-3 {className}" role="radiogroup" aria-label={label}>
    {#if label}
        <legend class="text-sm font-medium text-on-surface-muted">{label}</legend>
    {/if}

    <div class={orientationClasses[orientation]}>
        {@render children()}
    </div>

    {#if error}
        <p
            class="animate-in slide-in-from-top-1 fade-in text-sm text-danger-soft-text duration-200"
        >
            {error}
        </p>
    {:else if hint}
        <p class="text-sm text-on-surface-muted">{hint}</p>
    {/if}
</fieldset>
