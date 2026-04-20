<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Snippet } from "svelte";

    interface Props {
        children: Snippet;
        hoverable?: boolean;
        class?: string;
    }

    let { children, hoverable = false, class: className = "" }: Props = $props();
</script>

<tr
    class="border-b border-outline-faint {hoverable
        ? 'duration-fast transition-colors hover:bg-surface-sunken'
        : ''} {className}"
>
    {@render children()}
</tr>
