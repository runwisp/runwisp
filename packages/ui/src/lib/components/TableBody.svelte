<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Snippet } from "svelte";

    interface Props {
        children: Snippet;
        class?: string;
    }

    let { children, class: className = "" }: Props = $props();
</script>

<!-- The last row drops its divider: the frame below already draws that line. -->
<tbody class="[&>tr:last-child]:border-b-0 {className}">
    {@render children()}
</tbody>
