<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script module>
    import { defineMeta } from "@storybook/addon-svelte-csf";
    import Tooltip from "$lib/components/Tooltip.svelte";
    import Button from "$lib/components/Button.svelte";
    import { Info, CircleQuestionMark, Settings } from "@lucide/svelte";

    const { Story } = defineMeta({
        title: "Core/Tooltip",
        component: Tooltip,
        tags: ["autodocs"],
        argTypes: {
            position: {
                control: "select",
                options: ["top", "bottom", "left", "right"],
            },
        },
    });
</script>

<Story name="Top (Default)" asChild>
    <div class="flex justify-center py-8">
        <Tooltip content="This is a tooltip">
            <Button variant="secondary">Hover me</Button>
        </Tooltip>
    </div>
</Story>

<Story name="Positions" asChild>
    <div class="flex items-center justify-center gap-8 py-12">
        <Tooltip content="Top tooltip" position="top">
            <Button variant="secondary">Top</Button>
        </Tooltip>
        <Tooltip content="Bottom tooltip" position="bottom">
            <Button variant="secondary">Bottom</Button>
        </Tooltip>
        <Tooltip content="Left tooltip" position="left">
            <Button variant="secondary">Left</Button>
        </Tooltip>
        <Tooltip content="Right tooltip" position="right">
            <Button variant="secondary">Right</Button>
        </Tooltip>
    </div>
</Story>

<Story name="On Icons" asChild>
    <div class="flex items-center justify-center gap-6 py-8">
        <Tooltip content="More information">
            <button class="rounded-lg p-2 text-on-surface-muted hover:bg-surface-sunken">
                <Info size={20} />
            </button>
        </Tooltip>
        <Tooltip content="Get help">
            <button class="rounded-lg p-2 text-on-surface-muted hover:bg-surface-sunken">
                <CircleQuestionMark size={20} />
            </button>
        </Tooltip>
        <Tooltip content="Settings">
            <button class="rounded-lg p-2 text-on-surface-muted hover:bg-surface-sunken">
                <Settings size={20} />
            </button>
        </Tooltip>
    </div>
</Story>

<Story name="Usage Example" asChild>
    <div class="flex items-center gap-2 py-8">
        <span class="text-sm text-on-surface-muted">Cron Expression</span>
        <Tooltip content="Use standard cron syntax: minute hour day month weekday">
            <button class="text-on-surface-faint hover:text-on-surface-muted">
                <CircleQuestionMark size={16} />
            </button>
        </Tooltip>
    </div>
</Story>
