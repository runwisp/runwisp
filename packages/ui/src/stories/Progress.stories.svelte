<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script module>
    import { defineMeta } from "@storybook/addon-svelte-csf";
    import Progress from "$lib/components/Progress.svelte";

    const { Story } = defineMeta({
        title: "Core/Progress",
        component: Progress,
        tags: ["autodocs"],
        argTypes: {
            variant: {
                control: "select",
                options: ["default", "success", "warning", "danger"],
            },
            size: {
                control: "select",
                options: ["sm", "md", "lg"],
            },
        },
    });
</script>

<Story name="Default" args={{ value: 65 }} />

<Story name="With Label" args={{ value: 75, showLabel: true, label: "Upload progress" }} />

<Story name="Variants" asChild>
    <div class="max-w-md space-y-4">
        <Progress value={80} label="Default" showLabel />
        <Progress value={80} variant="success" label="Success" showLabel />
        <Progress value={80} variant="warning" label="Warning" showLabel />
        <Progress value={80} variant="danger" label="Danger" showLabel />
    </div>
</Story>

<Story name="Sizes" asChild>
    <div class="max-w-md space-y-4">
        <Progress value={60} size="sm" label="Small" showLabel />
        <Progress value={60} size="md" label="Medium" showLabel />
        <Progress value={60} size="lg" label="Large" showLabel />
    </div>
</Story>

<Story name="Usage Example" asChild>
    <div class="max-w-md space-y-4 rounded-xl border border-outline bg-white p-4">
        <h3 class="font-semibold text-on-surface">Resource Usage</h3>
        <Progress value={45} label="CPU Usage" showLabel />
        <Progress value={72} label="Memory" showLabel variant="warning" />
        <Progress value={23} label="Storage" showLabel variant="success" />
        <Progress value={89} label="API Requests" showLabel variant="danger" />
    </div>
</Story>

<Story name="Task Execution Progress" asChild>
    <div class="max-w-md space-y-4 rounded-xl border border-outline bg-white p-4">
        <div class="flex items-center justify-between">
            <h3 class="font-semibold text-on-surface">backup-database</h3>
            <span class="text-sm text-on-surface-muted">Running...</span>
        </div>
        <Progress value={67} showLabel />
        <p class="text-sm text-on-surface-muted">Processing 847 of 1,234 records</p>
    </div>
</Story>
