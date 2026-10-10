<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script module>
    import { defineMeta } from "@storybook/addon-svelte-csf";
    import { LifeBuoy, Pencil, Trash } from "@lucide/svelte";
    import Accordion from "$lib/components/Accordion.svelte";
    import AccordionItem from "$lib/components/AccordionItem.svelte";
    import Toggle from "$lib/components/Toggle.svelte";

    const blocks = [
        {
            label: "Node.js 22",
            enabled: true,
            frameClass: "border-outline bg-surface-raised",
            run: "RUN apk add --no-cache nodejs npm",
        },
        {
            label: "Python 3.12",
            enabled: false,
            frameClass: "border-outline-faint bg-surface",
            run: "RUN apk add --no-cache python3",
        },
        {
            label: "Go 1.27 (stale)",
            enabled: true,
            frameClass: "border-warning-soft-border bg-warning-soft/30",
            run: "RUN apk add --no-cache go",
        },
    ];

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

<!-- `leading` sits before the title and never toggles the panel; `frameClass`
     tints the frame by state. -->
<Story name="Framed with leading control and tint" asChild>
    <div class="flex max-w-lg flex-col gap-2">
        {#each blocks as block (block.label)}
            <AccordionItem
                variant="framed"
                frameClass={block.frameClass}
                class={block.enabled ? "" : "opacity-60"}
            >
                {#snippet leading()}
                    <Toggle checked={block.enabled} size="sm" />
                {/snippet}
                {#snippet title()}
                    <span class="block truncate">{block.label}</span>
                {/snippet}
                {#snippet actions()}
                    <button
                        type="button"
                        aria-label="Edit"
                        class="rounded-[3px] p-1 text-on-surface-faint hover:bg-surface-sunken hover:text-on-surface"
                    >
                        <Pencil size={14} />
                    </button>
                    <button
                        type="button"
                        aria-label="Remove"
                        class="rounded-[3px] p-1 text-on-surface-faint hover:bg-danger-soft hover:text-danger-surface"
                    >
                        <Trash size={14} />
                    </button>
                {/snippet}
                <pre class="font-mono text-xs">{block.run}</pre>
            </AccordionItem>
        {/each}
    </div>
</Story>
