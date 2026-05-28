<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script module>
    import { defineMeta } from "@storybook/addon-svelte-csf";
    import Logo from "$lib/components/Logo.svelte";

    const { Story } = defineMeta({
        title: "Brand/Logo",
        component: Logo,
        tags: ["autodocs"],
        argTypes: {
            size: {
                control: "select",
                options: ["xs", "sm", "md", "lg", "xl"],
            },
        },
    });
</script>

<Story name="Default" args={{}} />

<Story name="Sizes" asChild>
    <div class="flex items-center gap-8 rounded-lg bg-surface-sunken p-8">
        <div class="flex flex-col items-center gap-2">
            <Logo size="xs" />
            <span class="text-xs text-on-surface-muted">XS</span>
        </div>
        <div class="flex flex-col items-center gap-2">
            <Logo size="sm" />
            <span class="text-xs text-on-surface-muted">SM</span>
        </div>
        <div class="flex flex-col items-center gap-2">
            <Logo size="md" />
            <span class="text-xs text-on-surface-muted">MD</span>
        </div>
        <div class="flex flex-col items-center gap-2">
            <Logo size="lg" />
            <span class="text-xs text-on-surface-muted">LG</span>
        </div>
        <div class="flex flex-col items-center gap-2">
            <Logo size="xl" />
            <span class="text-xs text-on-surface-muted">XL</span>
        </div>
    </div>
</Story>

<Story name="On Gradient Background" asChild>
    <div
        class="flex items-center gap-4 rounded-lg bg-gradient-to-br from-wisp-500 to-aurora-500 p-8 text-white"
    >
        <Logo size="lg" />
        <span class="text-2xl font-bold">RunWisp</span>
    </div>
</Story>

<Story name="Dark Background" asChild>
    <div class="flex items-center gap-4 rounded-lg bg-mist-900 p-8 text-white">
        <Logo size="lg" />
        <span class="text-2xl font-bold">RunWisp</span>
    </div>
</Story>

<Story name="With Custom Class" asChild>
    <div class="flex items-center gap-4 rounded-lg bg-surface-sunken p-8">
        <Logo size="lg" class="text-primary" />
        <span class="text-lg font-semibold text-on-surface">Custom Color</span>
    </div>
</Story>

<Story name="In Header Style" asChild>
    <div
        class="flex items-center gap-4 rounded-lg bg-gradient-to-br from-wisp-500 to-aurora-500 p-4 text-white"
    >
        <div
            class="flex h-8 w-8 items-center justify-center rounded-lg bg-white/20 backdrop-blur-sm"
        >
            <Logo size="sm" />
        </div>
        <span class="text-lg font-semibold">RunWisp</span>
    </div>
</Story>
