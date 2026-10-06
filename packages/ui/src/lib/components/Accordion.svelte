<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { setContext, type Snippet } from "svelte";
    import { ACCORDION_KEY, AccordionGroup } from "./accordion.svelte.js";

    interface Props {
        /** `single` keeps at most one item open; `multiple` lets items open independently. */
        type?: "single" | "multiple";
        children?: Snippet;
        class?: string;
    }

    let { type = "multiple", children, class: className = "" }: Props = $props();

    setContext(ACCORDION_KEY, new AccordionGroup(() => type === "single"));
</script>

<div class="divide-y divide-outline {className}">
    {#if children}
        {@render children()}
    {/if}
</div>
