<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import { Badge } from "@runwisp/ui";
    import type { ComponentProps, Snippet } from "svelte";

    type BadgeProps = ComponentProps<typeof Badge>;

    interface Props {
        /** Shown as the native title tooltip — Badge itself doesn't take one. */
        tooltip: string;
        variant?: BadgeProps["variant"];
        size?: BadgeProps["size"];
        outline?: boolean;
        class?: string;
        children: Snippet;
    }

    let {
        tooltip,
        variant = "default",
        size = "md",
        outline = false,
        class: className = "",
        children,
    }: Props = $props();
</script>

<span title={tooltip}>
    <Badge {variant} {size} {outline} class={className}>
        {@render children()}
    </Badge>
</span>
