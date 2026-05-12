// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

export type ButtonVariant = "primary" | "secondary" | "ghost" | "danger" | "success";
export type ButtonSize = "xs" | "sm" | "md" | "lg";

export const BUTTON_BASE = `
    group
    inline-flex items-center justify-center gap-2
    font-medium cursor-pointer select-none
    border border-transparent
    focus-visible:outline-none focus-visible:ring-4 focus-visible:ring-ring/10 focus-visible:border-ring
    disabled:opacity-50 disabled:cursor-not-allowed
    transition-all duration-normal ease-out
    active:scale-[0.98]
`;

export const BUTTON_VARIANTS: Record<ButtonVariant, string> = {
    primary: `
        bg-primary text-on-primary shadow-sm shadow-primary/20
        hover:bg-primary-hover hover:shadow-md hover:shadow-primary/30
        active:bg-primary-active
    `,
    secondary: `
        bg-surface-raised text-on-surface-muted border-outline shadow-sm
        hover:bg-surface-sunken hover:text-on-surface hover:border-outline-hover
        active:bg-surface-sunken
    `,
    ghost: `
        bg-transparent text-on-surface-muted
        hover:bg-surface-sunken hover:text-on-surface
        active:bg-surface-sunken
    `,
    danger: `
        bg-danger-surface text-on-danger shadow-sm shadow-danger-surface/20
        hover:bg-danger-hover hover:shadow-md hover:shadow-danger-surface/30
        active:bg-danger-active
    `,
    success: `
        bg-success-surface text-on-success shadow-sm shadow-success-surface/20
        hover:bg-success-hover hover:shadow-md hover:shadow-success-surface/30
        active:bg-success-surface
    `,
};

export const BUTTON_SIZES: Record<ButtonSize, string> = {
    xs: "text-xs px-2 py-1 rounded-md",
    sm: "text-sm px-3 py-1.5 rounded-lg",
    md: "text-sm px-4 py-2 rounded-lg",
    lg: "text-base px-6 py-3 rounded-xl",
};
