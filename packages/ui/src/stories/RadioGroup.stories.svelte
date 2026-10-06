<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script module>
    import { defineMeta } from "@storybook/addon-svelte-csf";
    import Radio from "$lib/components/Radio.svelte";
    import RadioGroup from "$lib/components/RadioGroup.svelte";

    const { Story } = defineMeta({
        title: "Forms/RadioGroup",
        component: RadioGroup,
        tags: ["autodocs"],
    });
</script>

<script lang="ts">
    let policy = $state("queue");
</script>

<Story name="Default" asChild>
    <div class="space-y-3">
        <RadioGroup name="concurrency" label="When a run is already active" bind:value={policy}>
            <Radio value="queue" label="Queue" description="Start after the current run ends." />
            <Radio value="skip" label="Skip" description="Drop the new firing." />
            <Radio value="replace" label="Replace" description="Stop the current run first." />
        </RadioGroup>
        <p class="font-mono text-xs text-on-surface-muted">selected: {policy}</p>
    </div>
</Story>

<Story name="Horizontal" asChild>
    <RadioGroup
        name="theme"
        label="Theme"
        value="auto"
        orientation="horizontal"
        hint="Follows your OS by default."
    >
        <Radio value="auto" label="Auto" />
        <Radio value="light" label="Light" />
        <Radio value="dark" label="Dark" />
    </RadioGroup>
</Story>
