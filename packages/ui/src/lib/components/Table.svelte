<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Snippet } from "svelte";

    interface Props {
        children: Snippet;
        /** Render only the <table>, without the scrolling bordered frame, for a
         *  parent that supplies its own (as DataGrid does). */
        bare?: boolean;
        class?: string;
    }

    let { children, bare = false, class: className = "" }: Props = $props();
</script>

{#if bare}
    <table class="w-full text-sm {className}">
        {@render children()}
    </table>
{:else}
    <div class="overflow-x-auto rounded-[4px] border border-outline {className}">
        <table class="w-full text-sm">
            {@render children()}
        </table>
    </div>
{/if}
