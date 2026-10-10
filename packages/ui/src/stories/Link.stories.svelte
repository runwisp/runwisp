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
            variant: {
                control: "select",
                options: ["default", "muted", "primary", "danger", "unstyled"],
            },
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

<!-- `unstyled` adds nothing beyond `class`: the row brings its own layout and colours. -->
<Story name="Unstyled nav row" asChild>
    <nav class="w-64 space-y-0.5">
        <Link
            href="#tasks"
            variant="unstyled"
            aria-current="page"
            class="flex items-center gap-3 rounded-sm bg-primary-soft px-3 py-2 font-mono text-sm text-primary-soft-text"
        >
            Tasks
        </Link>
        <Link
            href="#runners"
            variant="unstyled"
            class="flex items-center gap-3 rounded-sm px-3 py-2 font-mono text-sm text-on-surface-muted hover:bg-surface-sunken hover:text-primary"
        >
            Runners
        </Link>
    </nav>
</Story>
