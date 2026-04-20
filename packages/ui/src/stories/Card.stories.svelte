<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script module>
    import { defineMeta } from "@storybook/addon-svelte-csf";
    import Card from "$lib/components/Card.svelte";
    import Button from "$lib/components/Button.svelte";
    import Badge from "$lib/components/Badge.svelte";

    const { Story } = defineMeta({
        title: "Core/Card",
        component: Card,
        tags: ["autodocs"],
        argTypes: {
            padding: {
                control: "select",
                options: ["none", "sm", "md", "lg"],
            },
            shadow: {
                control: "select",
                options: ["none", "sm", "md", "lg"],
            },
        },
    });
</script>

<Story name="Default" args={{}}>
    <h3 class="mb-2 text-lg font-semibold text-mist-900">Card Title</h3>
    <p class="text-mist-600">This is a basic card component with default padding and styling.</p>
</Story>

<Story name="With Header and Footer" asChild>
    <Card>
        {#snippet header()}
            <div class="flex items-center justify-between">
                <h3 class="font-semibold text-mist-900">Task Configuration</h3>
                <Badge variant="success" dot>Active</Badge>
            </div>
        {/snippet}

        <div class="space-y-3">
            <p class="text-sm text-mist-600">
                Configure your scheduled task settings and execution parameters.
            </p>
            <div class="text-sm">
                <span class="text-mist-500">Schedule:</span>
                <span class="ml-2 font-mono text-mist-800">*/5 * * * *</span>
            </div>
        </div>

        {#snippet footer()}
            <div class="flex justify-end gap-2">
                <Button variant="ghost" size="sm">Cancel</Button>
                <Button variant="primary" size="sm">Save Changes</Button>
            </div>
        {/snippet}
    </Card>
</Story>

<Story name="Hoverable" asChild>
    <div class="grid max-w-3xl grid-cols-3 gap-4">
        <Card hover>
            <h4 class="mb-1 font-semibold text-mist-900">Total Tasks</h4>
            <p class="text-2xl font-bold text-wisp-600">156</p>
        </Card>
        <Card hover>
            <h4 class="mb-1 font-semibold text-mist-900">Active</h4>
            <p class="text-2xl font-bold text-success-600">142</p>
        </Card>
        <Card hover>
            <h4 class="mb-1 font-semibold text-mist-900">Failed</h4>
            <p class="text-2xl font-bold text-danger-600">3</p>
        </Card>
    </div>
</Story>

<Story name="Different Padding" asChild>
    <div class="max-w-md space-y-4">
        <Card padding="sm">
            <p class="text-sm text-mist-600">Small padding card</p>
        </Card>
        <Card padding="md">
            <p class="text-sm text-mist-600">Medium padding card (default)</p>
        </Card>
        <Card padding="lg">
            <p class="text-sm text-mist-600">Large padding card</p>
        </Card>
    </div>
</Story>

<Story name="Different Shadows" asChild>
    <div class="flex gap-4">
        <Card shadow="none">
            <p class="text-sm text-mist-600">No shadow</p>
        </Card>
        <Card shadow="sm">
            <p class="text-sm text-mist-600">Small shadow</p>
        </Card>
        <Card shadow="md">
            <p class="text-sm text-mist-600">Medium shadow</p>
        </Card>
        <Card shadow="lg">
            <p class="text-sm text-mist-600">Large shadow</p>
        </Card>
    </div>
</Story>
