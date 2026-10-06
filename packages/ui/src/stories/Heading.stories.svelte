<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script module>
    import { defineMeta } from "@storybook/addon-svelte-csf";
    import Heading from "$lib/components/Heading.svelte";
    import Text from "$lib/components/Text.svelte";

    const { Story } = defineMeta({
        title: "Core/Heading",
        component: Heading,
        tags: ["autodocs"],
    });
</script>

<Story name="Levels" asChild>
    <div class="space-y-3">
        <Heading level={1}>Level 1</Heading>
        <Heading level={2}>Level 2</Heading>
        <Heading level={3}>Level 3</Heading>
        <Heading level={4}>Level 4</Heading>
    </div>
</Story>

<!-- A card title: the semantic level stays, the size shrinks. -->
<Story name="Card title" asChild>
    <div class="max-w-sm rounded-[4px] border border-outline bg-surface-raised p-4">
        <Heading level={2} size="sm" id="resources-title">System resources</Heading>
        <Text size="sm" color="muted" class="mt-2">CPU and memory over the last hour.</Text>
    </div>
</Story>
