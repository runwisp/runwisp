<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script module>
    import { defineMeta } from "@storybook/addon-svelte-csf";
    import Kbd from "$lib/components/Kbd.svelte";

    const { Story } = defineMeta({
        title: "Core/Kbd",
        component: Kbd,
        tags: ["autodocs"],
        argTypes: {
            size: { control: "select", options: ["xs", "sm"] },
            tone: { control: "select", options: ["default", "faint", "console"] },
        },
    });
</script>

<Story name="Default" args={{ keys: ["Ctrl", "K"] }} />

<Story name="Faint in an input" asChild>
    <div
        class="flex h-9 max-w-xs items-center justify-between rounded-[3px] border border-outline bg-surface-sunken/60 px-3 font-mono text-sm text-on-surface-faint"
    >
        Search tasks…
        <Kbd keys="⌘K" size="xs" tone="faint" />
    </div>
</Story>

<Story name="Console" asChild>
    <div
        class="flex items-center gap-1.5 bg-[var(--rw-con-bg)] px-3 py-2 font-mono text-xs text-[var(--rw-con-dim)]"
    >
        Restore <Kbd keys="Esc" size="xs" tone="console" />
    </div>
</Story>
