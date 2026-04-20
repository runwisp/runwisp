<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script module>
    import { defineMeta } from "@storybook/addon-svelte-csf";
    import Input from "$lib/components/Input.svelte";
    import { Search, Mail, Lock, User, Calendar, Clock } from "@lucide/svelte";

    const { Story } = defineMeta({
        title: "Forms/Input",
        component: Input,
        tags: ["autodocs"],
        argTypes: {
            size: {
                control: "select",
                options: ["sm", "md", "lg"],
            },
        },
    });
</script>

<Story name="Default" args={{ placeholder: "Enter text..." }} />

<Story name="With Label" args={{ label: "Task Name", placeholder: "my-scheduled-task" }} />

<Story
    name="With Hint"
    args={{
        label: "Cron Expression",
        placeholder: "*/5 * * * *",
        hint: "Use standard cron syntax. Minimum interval is 1 minute.",
    }}
/>

<Story
    name="With Error"
    args={{
        label: "Email Address",
        placeholder: "user@example.com",
        value: "invalid-email",
        error: "Please enter a valid email address",
    }}
/>

<Story name="With Icons" asChild>
    <div class="max-w-sm space-y-4">
        <Input placeholder="Search tasks...">
            {#snippet leadingIcon()}<Search size={18} />{/snippet}
        </Input>
        <Input label="Email" type="email" placeholder="you@example.com">
            {#snippet leadingIcon()}<Mail size={18} />{/snippet}
        </Input>
        <Input label="Password" type="password" placeholder="Enter password">
            {#snippet leadingIcon()}<Lock size={18} />{/snippet}
        </Input>
        <Input label="Username" placeholder="Enter username">
            {#snippet leadingIcon()}<User size={18} />{/snippet}
        </Input>
    </div>
</Story>

<Story name="Sizes" asChild>
    <div class="max-w-sm space-y-4">
        <Input size="sm" label="Small" placeholder="Small input" />
        <Input size="md" label="Medium" placeholder="Medium input (default)" />
        <Input size="lg" label="Large" placeholder="Large input" />
    </div>
</Story>

<Story
    name="Disabled"
    args={{ label: "Disabled Input", value: "Cannot edit this", disabled: true }}
/>

<Story name="Input Types" asChild>
    <div class="max-w-sm space-y-4">
        <Input label="Text" type="text" placeholder="Regular text" />
        <Input label="Email" type="email" placeholder="user@example.com" />
        <Input label="Password" type="password" placeholder="••••••••" />
        <Input label="Number" type="number" placeholder="0" />
        <Input label="Date" type="date">
            {#snippet leadingIcon()}<Calendar size={18} />{/snippet}
        </Input>
        <Input label="Time" type="time">
            {#snippet leadingIcon()}<Clock size={18} />{/snippet}
        </Input>
    </div>
</Story>
