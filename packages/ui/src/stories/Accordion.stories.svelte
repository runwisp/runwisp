<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script module>
    import { defineMeta } from "@storybook/addon-svelte-csf";
    import { LifeBuoy, Trash } from "@lucide/svelte";
    import Accordion from "$lib/components/Accordion.svelte";
    import AccordionItem from "$lib/components/AccordionItem.svelte";
    import Toggle from "$lib/components/Toggle.svelte";

    const { Story } = defineMeta({
        title: "Navigation/Accordion",
        component: Accordion,
        tags: ["autodocs"],
    });
</script>

<Story name="Multiple" asChild>
    <Accordion class="max-w-lg">
        <AccordionItem title="Retries" open>Retry a failed run up to three times.</AccordionItem>
        <AccordionItem title="Timeouts">Kill the run after 10 minutes.</AccordionItem>
        <AccordionItem title="Notifications">Send failures to Slack.</AccordionItem>
    </Accordion>
</Story>

<Story name="Single" asChild>
    <Accordion type="single" class="max-w-lg">
        <AccordionItem title="Retries" open>Opening another section closes this one.</AccordionItem>
        <AccordionItem title="Timeouts">Kill the run after 10 minutes.</AccordionItem>
        <AccordionItem title="Notifications">Send failures to Slack.</AccordionItem>
    </Accordion>
</Story>

<Story name="Framed, default open" asChild>
    <AccordionItem variant="framed" open class="max-w-lg">
        {#snippet title()}
            <span class="flex items-center gap-2.5">
                <LifeBuoy size={16} class="text-on-surface-faint" />
                Schedule recovery
                <span class="font-sans text-xs text-on-surface-faint">Missed ticks & jitter</span>
            </span>
        {/snippet}
        What to do with cron ticks missed while the scheduler was offline.
    </AccordionItem>
</Story>

<Story name="Framed with actions" asChild>
    <AccordionItem variant="framed" title="Node.js 22" class="max-w-lg">
        {#snippet actions()}
            <Toggle checked size="sm" />
            <button
                type="button"
                aria-label="Remove"
                class="text-on-surface-faint hover:text-danger-surface"
            >
                <Trash size={14} />
            </button>
        {/snippet}
        <pre class="font-mono text-xs">RUN apk add --no-cache nodejs npm</pre>
    </AccordionItem>
</Story>
