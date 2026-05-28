<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script module>
    import { defineMeta } from "@storybook/addon-svelte-csf";
    import Checkbox from "$lib/components/Checkbox.svelte";

    const { Story } = defineMeta({
        title: "Forms/Checkbox",
        component: Checkbox,
        tags: ["autodocs"],
        argTypes: {
            size: {
                control: "select",
                options: ["sm", "md", "lg"],
            },
        },
    });
</script>

<Story name="Default" />

<Story name="With Label" args={{ label: "Enable notifications" }} />

<Story
    name="With Description"
    args={{
        label: "Email notifications",
        description: "Receive email alerts when tasks fail or complete",
    }}
/>

<Story name="Checked" args={{ label: "Auto-retry failed tasks", checked: true }} />

<Story
    name="Indeterminate"
    args={{ label: "Select all tasks", indeterminate: true, checked: true }}
/>

<Story name="Sizes" asChild>
    <div class="space-y-4">
        <Checkbox size="sm" label="Small checkbox" />
        <Checkbox size="md" label="Medium checkbox (default)" />
        <Checkbox size="lg" label="Large checkbox" />
    </div>
</Story>

<Story name="Disabled" asChild>
    <div class="space-y-4">
        <Checkbox label="Unchecked disabled" disabled />
        <Checkbox label="Checked disabled" checked disabled />
    </div>
</Story>

<Story name="Settings Example" asChild>
    <div class="max-w-md space-y-4 rounded-xl border border-outline bg-white p-4">
        <h3 class="font-semibold text-on-surface">Notification Preferences</h3>
        <div class="space-y-3">
            <Checkbox
                label="Task completion alerts"
                description="Get notified when scheduled tasks complete successfully"
                checked
            />
            <Checkbox
                label="Failure alerts"
                description="Receive immediate notifications on task failures"
                checked
            />
            <Checkbox label="Weekly digest" description="Weekly summary of all task executions" />
            <Checkbox
                label="Performance warnings"
                description="Alert when task execution time exceeds threshold"
                checked
            />
        </div>
    </div>
</Story>
