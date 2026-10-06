<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script module>
    import { defineMeta } from "@storybook/addon-svelte-csf";
    import Link from "$lib/components/Link.svelte";

    const { Story } = defineMeta({
        title: "Core/Link",
        component: Link,
        tags: ["autodocs"],
        argTypes: {
            variant: { control: "select", options: ["default", "muted", "primary", "danger"] },
            underline: { control: "select", options: ["always", "hover", "none"] },
        },
    });
</script>

<Story name="Variants" asChild>
    <div class="flex flex-col gap-2 text-sm">
        <Link href="#tasks">Default</Link>
        <Link href="#tasks" variant="muted">Muted</Link>
        <Link href="#tasks" variant="primary">Primary</Link>
        <Link href="#tasks" variant="danger">Danger</Link>
    </div>
</Story>

<Story name="External" asChild>
    <Link
        href="https://docs.runwisp.com/configuration/tasks/"
        external
        variant="primary"
        title="Opens in a new tab"
        class="text-sm font-medium"
    >
        Task configuration docs →
    </Link>
</Story>
