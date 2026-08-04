// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

export type ButtonVariant = "primary" | "secondary" | "ghost" | "danger" | "success";
export type ButtonSize = "xs" | "sm" | "md" | "lg";

export const BUTTON_BASE = `
    group
    inline-flex items-center justify-center gap-2
    font-mono font-medium tracking-normal
    cursor-pointer select-none border
    focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2
    disabled:opacity-50 disabled:cursor-not-allowed
        active:translate-y-px
`;

export const BUTTON_VARIANTS: Record<ButtonVariant, string> = {
    primary: `
        bg-primary text-on-primary border-transparent
        hover:bg-primary-hover
        active:bg-primary-active
    `,
    secondary: `
        bg-surface-raised text-on-surface border-outline
        hover:border-outline-hover hover:text-primary
        active:bg-surface-sunken
    `,
    ghost: `
        bg-transparent text-on-surface-muted border-transparent
        hover:bg-surface-sunken hover:text-on-surface
        active:bg-surface-sunken
    `,
    danger: `
        bg-danger-surface text-on-danger border-transparent
        hover:bg-danger-hover
        active:bg-danger-active
    `,
    success: `
        bg-success-surface text-on-success border-transparent
        hover:bg-success-hover
        active:bg-success-surface
    `,
};

export const BUTTON_SIZES: Record<ButtonSize, string> = {
    xs: "text-xs px-2.5 py-1 rounded-[3px]",
    sm: "text-sm px-3 py-1.5 rounded-[3px]",
    md: "text-sm px-4 py-2 rounded-[3px]",
    lg: "text-base px-6 py-2.5 rounded-[3px]",
};
