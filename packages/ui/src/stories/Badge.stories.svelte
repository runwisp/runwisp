<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script module>
    import { defineMeta } from "@storybook/addon-svelte-csf";
    import Badge from "$lib/components/Badge.svelte";

    const { Story } = defineMeta({
        title: "Core/Badge",
        component: Badge,
        tags: ["autodocs"],
        argTypes: {
            variant: {
                control: "select",
                options: ["default", "primary", "success", "warning", "danger", "info"],
            },
            size: {
                control: "select",
                options: ["sm", "md", "lg"],
            },
        },
    });
</script>

<Story name="Default" args={{ variant: "default" }}>Default</Story>

<Story name="All Variants" asChild>
    <div class="flex flex-wrap items-center gap-2">
        <Badge variant="default">Default</Badge>
        <Badge variant="primary">Primary</Badge>
        <Badge variant="success">Success</Badge>
        <Badge variant="warning">Warning</Badge>
        <Badge variant="danger">Danger</Badge>
        <Badge variant="info">Info</Badge>
    </div>
</Story>

<Story name="Outline Variants" asChild>
    <div class="flex flex-wrap items-center gap-2">
        <Badge variant="default" outline>Default</Badge>
        <Badge variant="primary" outline>Primary</Badge>
        <Badge variant="success" outline>Success</Badge>
        <Badge variant="warning" outline>Warning</Badge>
        <Badge variant="danger" outline>Danger</Badge>
        <Badge variant="info" outline>Info</Badge>
    </div>
</Story>

<Story name="With Dot Indicator" asChild>
    <div class="flex flex-wrap items-center gap-2">
        <Badge variant="default" dot>Inactive</Badge>
        <Badge variant="primary" dot>Scheduled</Badge>
        <Badge variant="success" dot>Running</Badge>
        <Badge variant="warning" dot>Pending</Badge>
        <Badge variant="danger" dot>Failed</Badge>
        <Badge variant="info" dot>Processing</Badge>
    </div>
</Story>

<Story name="Sizes" asChild>
    <div class="flex flex-wrap items-center gap-2">
        <Badge size="sm" variant="primary">Small</Badge>
        <Badge size="md" variant="primary">Medium</Badge>
        <Badge size="lg" variant="primary">Large</Badge>
    </div>
</Story>

<Story name="Task Status Example" asChild>
    <div class="flex flex-wrap items-center gap-2">
        <Badge variant="success" dot>Completed</Badge>
        <Badge variant="primary" dot>Running</Badge>
        <Badge variant="warning" dot>Queued</Badge>
        <Badge variant="danger" dot>Failed</Badge>
        <Badge variant="default" dot>Cancelled</Badge>
    </div>
</Story>
