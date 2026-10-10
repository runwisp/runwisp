<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Snippet } from "svelte";
    import type { HTMLAnchorAttributes } from "svelte/elements";

    type LinkVariant = "default" | "muted" | "primary" | "danger" | "unstyled";
    type LinkUnderline = "always" | "hover" | "none";

    // Use Link for off-site URLs (`external` opens a new tab with rel=noopener
    // noreferrer). `unstyled` adds no classes beyond `class` (no `underline`
    // either), for nav rows and menu items that bring their own layout. An
    // in-app href must still come from SvelteKit's resolve(): being a
    // component, Link sits outside svelte/no-navigation-without-resolve.
    interface Props extends Omit<HTMLAnchorAttributes, "href" | "children" | "class"> {
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
        ...restProps
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
        unstyled: "",
    };

    const underlineClasses: Record<LinkUnderline, string> = {
        always: "underline underline-offset-2",
        hover: "hover:underline underline-offset-2",
        none: "no-underline",
    };
</script>

<a
    {href}
    class={variant === "unstyled"
        ? className
        : `${baseClasses} ${variantClasses[variant]} ${underlineClasses[underline]} ${className}`}
    target={external ? "_blank" : undefined}
    rel={external ? "noopener noreferrer" : undefined}
    {...restProps}
>
    {@render children()}
</a>
