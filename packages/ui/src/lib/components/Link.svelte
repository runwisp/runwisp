<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Snippet } from "svelte";

    type LinkVariant = "default" | "muted" | "primary" | "danger";
    type LinkUnderline = "always" | "hover" | "none";

    interface Props {
        href: string;
        variant?: LinkVariant;
        external?: boolean;
        underline?: LinkUnderline;
        children: Snippet;
        class?: string;
    }

    let {
        href,
        variant = "default",
        external = false,
        underline = "hover",
        children,
        class: className = "",
    }: Props = $props();

    const baseClasses = `
        inline-flex items-center gap-1
                focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2
        rounded-[3px]
    `;

    const variantClasses: Record<LinkVariant, string> = {
        default: "text-on-surface hover:text-primary",
        muted: "text-on-surface-muted hover:text-on-surface",
        primary: "text-primary hover:text-primary-hover",
        danger: "text-danger-surface hover:text-danger-hover",
    };

    const underlineClasses: Record<LinkUnderline, string> = {
        always: "underline underline-offset-2",
        hover: "hover:underline underline-offset-2",
        none: "no-underline",
    };
</script>

<a
    {href}
    class="{baseClasses} {variantClasses[variant]} {underlineClasses[underline]} {className}"
    target={external ? "_blank" : undefined}
    rel={external ? "noopener noreferrer" : undefined}
>
    {@render children()}
</a>
