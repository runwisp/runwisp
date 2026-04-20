<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script module>
    import { defineMeta } from "@storybook/addon-svelte-csf";
    import Avatar from "$lib/components/Avatar.svelte";

    const { Story } = defineMeta({
        title: "Core/Avatar",
        component: Avatar,
        tags: ["autodocs"],
        argTypes: {
            size: {
                control: "select",
                options: ["xs", "sm", "md", "lg", "xl"],
            },
            status: {
                control: "select",
                options: [undefined, "online", "offline", "busy", "away"],
            },
        },
    });
</script>

<Story name="Default" args={{ name: "John Doe" }} />

<Story
    name="With Image"
    args={{
        name: "Jane Smith",
        src: "https://images.unsplash.com/photo-1494790108377-be9c29b29330?w=100&h=100&fit=crop",
    }}
/>

<Story name="Sizes" asChild>
    <div class="flex items-center gap-4">
        <Avatar name="John Doe" size="xs" />
        <Avatar name="John Doe" size="sm" />
        <Avatar name="John Doe" size="md" />
        <Avatar name="John Doe" size="lg" />
        <Avatar name="John Doe" size="xl" />
    </div>
</Story>

<Story name="With Status" asChild>
    <div class="flex items-center gap-4">
        <Avatar name="Online User" status="online" />
        <Avatar name="Offline User" status="offline" />
        <Avatar name="Busy User" status="busy" />
        <Avatar name="Away User" status="away" />
    </div>
</Story>

<Story name="Different Names" asChild>
    <div class="flex items-center gap-4">
        <Avatar name="Alice" />
        <Avatar name="Bob Chen" />
        <Avatar name="Charlie Davis" />
        <Avatar name="Diana Evans" />
        <Avatar name="Eve Franklin" />
    </div>
</Story>

<Story name="Team Members" asChild>
    <div class="flex items-center -space-x-3">
        <Avatar name="Alice Johnson" size="md" />
        <Avatar name="Bob Smith" size="md" />
        <Avatar name="Charlie Brown" size="md" />
        <Avatar name="Diana Ross" size="md" />
        <div
            class="flex h-10 w-10 items-center justify-center rounded-full bg-mist-200 text-sm font-medium text-mist-600 ring-2 ring-white"
        >
            +5
        </div>
    </div>
</Story>
