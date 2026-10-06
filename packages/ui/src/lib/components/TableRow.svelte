<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Snippet } from "svelte";
    import type { HTMLAttributes } from "svelte/elements";

    type RowBorder = "default" | "faint" | "none";

    interface Props extends Omit<HTMLAttributes<HTMLTableRowElement>, "children" | "class"> {
        children: Snippet;
        hoverable?: boolean;
        /** Bottom divider: `default` under a header, `faint` between body rows. */
        border?: RowBorder;
        class?: string;
    }

    let {
        children,
        hoverable = false,
        border = "default",
        class: className = "",
        ...restProps
    }: Props = $props();

    const borderClasses: Record<RowBorder, string> = {
        default: "border-b border-outline",
        faint: "border-b border-outline-faint",
        none: "",
    };
</script>

<tr
    class="{borderClasses[border]} {hoverable ? 'hover:bg-surface-sunken' : ''} {className}"
    {...restProps}
>
    {@render children()}
</tr>
