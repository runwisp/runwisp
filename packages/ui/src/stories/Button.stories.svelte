<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script module>
    import { defineMeta } from "@storybook/addon-svelte-csf";
    import Button from "$lib/components/Button.svelte";
    import { Play, Plus, ArrowRight, Download, Trash2 } from "@lucide/svelte";

    const { Story } = defineMeta({
        title: "Core/Button",
        component: Button,
        tags: ["autodocs"],
        argTypes: {
            variant: {
                control: "select",
                options: ["primary", "secondary", "ghost", "danger", "success"],
            },
            size: {
                control: "select",
                options: ["xs", "sm", "md", "lg"],
            },
        },
    });
</script>

<Story name="Primary" args={{ variant: "primary" }}>Primary Button</Story>

<Story name="Secondary" args={{ variant: "secondary" }}>Secondary Button</Story>

<Story name="Ghost" args={{ variant: "ghost" }}>Ghost Button</Story>

<Story name="Danger" args={{ variant: "danger" }}>Danger Button</Story>

<Story name="Success" args={{ variant: "success" }}>Success Button</Story>

<Story name="All Variants" asChild>
    <div class="flex flex-wrap items-center gap-3">
        <Button variant="primary">Primary</Button>
        <Button variant="secondary">Secondary</Button>
        <Button variant="ghost">Ghost</Button>
        <Button variant="danger">Danger</Button>
        <Button variant="success">Success</Button>
    </div>
</Story>

<Story name="Sizes" asChild>
    <div class="flex flex-wrap items-center gap-3">
        <Button size="xs">Extra Small</Button>
        <Button size="sm">Small</Button>
        <Button size="md">Medium</Button>
        <Button size="lg">Large</Button>
    </div>
</Story>

<Story name="With Icons" asChild>
    <div class="flex flex-wrap items-center gap-3">
        <Button variant="primary">
            {#snippet icon()}<Play size={16} />{/snippet}
            Run Task
        </Button>
        <Button variant="secondary">
            {#snippet icon()}<Plus size={16} />{/snippet}
            Add Schedule
        </Button>
        <Button variant="ghost">
            {#snippet iconRight()}<ArrowRight size={16} />{/snippet}
            View Details
        </Button>
        <Button variant="primary">
            {#snippet icon()}<Download size={16} />{/snippet}
            Export
        </Button>
        <Button variant="danger">
            {#snippet icon()}<Trash2 size={16} />{/snippet}
            Delete
        </Button>
    </div>
</Story>

<Story name="Loading State" asChild>
    <div class="flex flex-wrap items-center gap-3">
        <Button variant="primary" loading>Loading...</Button>
        <Button variant="secondary" loading>Processing</Button>
        <Button variant="danger" loading>Deleting</Button>
    </div>
</Story>

<Story name="Disabled" asChild>
    <div class="flex flex-wrap items-center gap-3">
        <Button variant="primary" disabled>Disabled Primary</Button>
        <Button variant="secondary" disabled>Disabled Secondary</Button>
        <Button variant="ghost" disabled>Disabled Ghost</Button>
    </div>
</Story>

<Story name="Full Width" asChild>
    <div class="max-w-sm space-y-3">
        <Button variant="primary" fullWidth>Full Width Primary</Button>
        <Button variant="secondary" fullWidth>Full Width Secondary</Button>
    </div>
</Story>
