<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Snippet } from "svelte";
    import TableRow from "./TableRow.svelte";

    interface Props {
        children: Snippet;
        /** Wrap children in one header row. Turn off to pass your own
         *  TableRow(s), e.g. for a two-row header. */
        row?: boolean;
        class?: string;
    }

    let { children, row = true, class: className = "" }: Props = $props();
</script>

<thead class={className}>
    {#if row}
        <TableRow>
            {@render children()}
        </TableRow>
    {:else}
        {@render children()}
    {/if}
</thead>
