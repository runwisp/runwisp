<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script module>
    import { defineMeta } from "@storybook/addon-svelte-csf";
    import Dropdown from "$lib/components/Dropdown.svelte";
    import { Play, Pause, SquarePen, Copy, Trash2, Settings, ChevronDown } from "@lucide/svelte";

    const { Story } = defineMeta({
        title: "Core/Dropdown",
        component: Dropdown,
        tags: ["autodocs"],
        argTypes: {
            align: {
                control: "select",
                options: ["left", "right"],
            },
        },
    });

    const taskActions = [
        { label: "Run Now", icon: Play, onClick: () => console.log("Run") },
        { label: "Edit", icon: SquarePen, onClick: () => console.log("Edit") },
        { label: "Duplicate", icon: Copy, onClick: () => console.log("Duplicate") },
        { label: "Pause", icon: Pause, onClick: () => console.log("Pause") },
        { divider: true },
        { label: "Delete", icon: Trash2, danger: true, onClick: () => console.log("Delete") },
    ];

    const settingsMenu = [
        { label: "Profile Settings", href: "/settings/profile" },
        { label: "Team Settings", href: "/settings/team" },
        { label: "Notifications", href: "/settings/notifications" },
        { divider: true },
        { label: "API Keys", href: "/settings/api" },
        { label: "Billing", href: "/settings/billing" },
    ];
</script>

<Story name="Default" args={{ items: taskActions }} />

<Story name="Custom Trigger" asChild>
    <div class="flex justify-center py-8">
        <Dropdown items={settingsMenu}>
            {#snippet trigger()}
                <div class="flex items-center gap-2">
                    <Settings size={18} />
                    <span class="text-sm">Settings</span>
                    <ChevronDown size={16} />
                </div>
            {/snippet}
        </Dropdown>
    </div>
</Story>

<Story name="Align Left" args={{ items: taskActions, align: "left" }} />

<Story
    name="With Disabled Items"
    args={{
        items: [
            { label: "Run Now", icon: Play },
            { label: "Edit", icon: SquarePen },
            { label: "Duplicate", icon: Copy, disabled: true },
            { divider: true },
            { label: "Delete", icon: Trash2, danger: true, disabled: true },
        ],
    }}
/>

<Story name="In Context" asChild>
    <div class="max-w-md rounded-xl border border-mist-200 bg-white p-4">
        <div class="flex items-center justify-between">
            <div>
                <h3 class="font-semibold text-mist-900">backup-database</h3>
                <p class="text-sm text-mist-500">Daily at 2:00 AM</p>
            </div>
            <Dropdown items={taskActions} />
        </div>
    </div>
</Story>
