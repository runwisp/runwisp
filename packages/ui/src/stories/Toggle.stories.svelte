<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script module>
    import { defineMeta } from "@storybook/addon-svelte-csf";
    import Toggle from "$lib/components/Toggle.svelte";

    const { Story } = defineMeta({
        title: "Forms/Toggle",
        component: Toggle,
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

<Story name="With Label" args={{ label: "Enable scheduling" }} />

<Story
    name="With Description"
    args={{
        label: "Auto-scaling",
        description: "Automatically scale workers based on queue depth",
    }}
/>

<Story name="Checked" args={{ label: "Active", checked: true }} />

<Story name="Sizes" asChild>
    <div class="space-y-4">
        <Toggle size="sm" label="Small toggle" />
        <Toggle size="md" label="Medium toggle (default)" />
        <Toggle size="lg" label="Large toggle" />
    </div>
</Story>

<Story name="Disabled" asChild>
    <div class="space-y-4">
        <Toggle label="Disabled off" disabled />
        <Toggle label="Disabled on" checked disabled />
    </div>
</Story>

<Story name="Settings Panel" asChild>
    <div class="max-w-md space-y-4 rounded-xl border border-mist-200 bg-white p-4">
        <h3 class="font-semibold text-mist-900">Task Settings</h3>
        <div class="space-y-4">
            <Toggle
                label="Enable task"
                description="When disabled, the task will not run on schedule"
                checked
            />
            <Toggle
                label="Auto-retry"
                description="Automatically retry failed executions up to 3 times"
                checked
            />
            <Toggle
                label="Concurrent execution"
                description="Allow multiple instances to run simultaneously"
            />
            <Toggle
                label="Debug logging"
                description="Enable verbose logging for troubleshooting"
            />
        </div>
    </div>
</Story>
