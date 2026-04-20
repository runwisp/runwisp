<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Snippet } from "svelte";

    interface Props {
        type?: "single" | "multiple";
        children?: Snippet;
        class?: string;
    }

    let { type = "multiple", children, class: className = "" }: Props = $props();
</script>

<div class="divide-y divide-outline {className}" role="presentation" data-accordion-type={type}>
    {#if children}
        {@render children()}
    {/if}
</div>
