<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script module>
    import { defineMeta } from "@storybook/addon-svelte-csf";
    import Tabs from "$lib/components/Tabs.svelte";
    import { Calendar, ListTodo, Activity, Settings } from "@lucide/svelte";

    const { Story } = defineMeta({
        title: "Core/Tabs",
        component: Tabs,
        tags: ["autodocs"],
        argTypes: {
            variant: {
                control: "select",
                options: ["underline", "pills", "enclosed"],
            },
        },
    });

    const basicTabs = [
        { id: "overview", label: "Overview" },
        { id: "details", label: "Details" },
        { id: "logs", label: "Logs" },
        { id: "settings", label: "Settings" },
    ];

    const iconTabs = [
        { id: "schedules", label: "Schedules", icon: Calendar },
        { id: "tasks", label: "Tasks", icon: ListTodo, badge: 3 },
        { id: "monitoring", label: "Monitoring", icon: Activity },
        { id: "settings", label: "Settings", icon: Settings },
    ];

    const disabledTabs = [
        { id: "active", label: "Active" },
        { id: "completed", label: "Completed" },
        { id: "failed", label: "Failed" },
        { id: "archived", label: "Archived", disabled: true },
    ];
</script>

<Story name="Underline (Default)" asChild>
    <Tabs tabs={basicTabs} variant="underline">
        {#snippet children(activeTab)}
            <div class="rounded-lg border border-outline bg-white p-4">
                <p class="text-on-surface-muted">Content for <strong>{activeTab}</strong> tab</p>
            </div>
        {/snippet}
    </Tabs>
</Story>

<Story name="Pills" asChild>
    <Tabs tabs={basicTabs} variant="pills">
        {#snippet children(activeTab)}
            <div class="rounded-lg border border-outline bg-white p-4">
                <p class="text-on-surface-muted">Content for <strong>{activeTab}</strong> tab</p>
            </div>
        {/snippet}
    </Tabs>
</Story>

<Story name="Enclosed" asChild>
    <Tabs tabs={basicTabs} variant="enclosed">
        {#snippet children(activeTab)}
            <div class="rounded-lg border border-outline bg-white p-4">
                <p class="text-on-surface-muted">Content for <strong>{activeTab}</strong> tab</p>
            </div>
        {/snippet}
    </Tabs>
</Story>

<Story name="With Icons and Badges" asChild>
    <Tabs tabs={iconTabs} variant="underline">
        {#snippet children(activeTab)}
            <div class="rounded-lg border border-outline bg-white p-4">
                <p class="text-on-surface-muted">
                    You're viewing the <strong>{activeTab}</strong> section
                </p>
            </div>
        {/snippet}
    </Tabs>
</Story>

<Story name="Full Width" asChild>
    <div class="max-w-md">
        <Tabs tabs={basicTabs} variant="pills" fullWidth>
            {#snippet children(activeTab)}
                <div class="rounded-lg border border-outline bg-white p-4">
                    <p class="text-on-surface-muted">
                        Content for <strong>{activeTab}</strong> tab
                    </p>
                </div>
            {/snippet}
        </Tabs>
    </div>
</Story>

<Story name="With Disabled Tab" asChild>
    <Tabs tabs={disabledTabs} variant="underline">
        {#snippet children(activeTab)}
            <div class="rounded-lg border border-outline bg-white p-4">
                <p class="text-on-surface-muted">Showing <strong>{activeTab}</strong> tasks</p>
            </div>
        {/snippet}
    </Tabs>
</Story>
