<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script module>
    import { defineMeta } from "@storybook/addon-svelte-csf";
    import Textarea from "$lib/components/Textarea.svelte";

    const { Story } = defineMeta({
        title: "Forms/Textarea",
        component: Textarea,
        tags: ["autodocs"],
    });
</script>

<Story name="Default" args={{ placeholder: "Enter description..." }} />

<Story
    name="With Label"
    args={{ label: "Task Description", placeholder: "Describe what this task does...", rows: 4 }}
/>

<Story
    name="With Hint"
    args={{
        label: "Script Content",
        placeholder: "Enter your script here...",
        hint: "Enter the shell script to execute.",
        rows: 6,
    }}
/>

<Story
    name="With Error"
    args={{ label: "Configuration", error: "Invalid syntax on line 1", rows: 4 }}
/>

<Story
    name="Disabled"
    args={{
        label: "Read-only logs",
        value: "Task completed successfully",
        disabled: true,
        rows: 4,
    }}
/>
