<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<!-- First-load placeholder for the overview: the same grid, cards and headings
     as OverviewPage, with pulsing bars where the numbers and rows will land. -->
<script lang="ts">
    import { PageContainer, Card, Heading } from "@runwisp/ui";

    const STAT_LABELS = ["healthy tasks", "uptime", "total runs", "recent success"];
    const PANEL_TITLES = ["Needs attention", "Running now", "Up next"];
</script>

{#snippet bar(cls: string)}
    <span class="block animate-pulse rounded-[3px] bg-outline-hover {cls}"></span>
{/snippet}

<PageContainer variant="wide" class="space-y-5">
    <div
        class="grid gap-5 xl:grid-cols-[minmax(0,1fr)_320px]"
        role="status"
        aria-label="Loading overview"
    >
        <div class="flex flex-col gap-5">
            <div class="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
                {#each STAT_LABELS as label (label)}
                    <div
                        class="relative rounded-[4px] border border-outline bg-surface-raised shadow-sm"
                    >
                        <span
                            class="absolute top-0 left-3.5 -translate-y-1/2 bg-surface-sunken px-2 font-mono text-[10.5px] leading-[1.6] font-medium tracking-[0.06em] text-on-surface-muted"
                            >{label}</span
                        >
                        <div class="px-4 pt-5 pb-4">{@render bar("h-7 w-20")}</div>
                        <div class="space-y-1.5 border-t border-outline-faint px-4 py-2.5">
                            {@render bar("h-2.5 w-4/5")}
                            {@render bar("h-2.5 w-1/2")}
                        </div>
                    </div>
                {/each}
            </div>

            <div class="grid flex-1 gap-4 md:grid-cols-3">
                {#each PANEL_TITLES as title (title)}
                    <Card>
                        <div class="flex items-center justify-between gap-3">
                            <Heading level={3} size="sm">{title}</Heading>
                            {@render bar("h-6 w-6")}
                        </div>
                        <div class="mt-4 space-y-2">
                            {#each [0, 1] as i (i)}
                                <div
                                    class="space-y-2 rounded-[3px] border border-outline-faint p-3"
                                >
                                    {@render bar("h-3 w-3/5")}
                                    {@render bar("h-2.5 w-2/5")}
                                </div>
                            {/each}
                        </div>
                    </Card>
                {/each}
            </div>
        </div>

        <div class="flex flex-col gap-5">
            <Card padding="lg">
                <div class="flex items-center justify-between gap-3">
                    <Heading level={2} size="sm">System resources</Heading>
                    {@render bar("h-6 w-16")}
                </div>
                <div class="mt-4 space-y-4">
                    {#each ["CPU", "Memory"] as label (label)}
                        <div>
                            <span
                                class="mb-1.5 block font-mono text-xs tracking-wide text-on-surface-muted"
                                >{label}</span
                            >
                            {@render bar("h-11 w-full")}
                        </div>
                    {/each}
                </div>
            </Card>

            <Card padding="lg">
                <div class="flex items-center justify-between gap-3">
                    <Heading level={2} size="sm">Recent activity</Heading>
                    {@render bar("h-3 w-14")}
                </div>
                <div class="mt-4 space-y-1.5">
                    {#each [0, 1, 2, 3, 4] as i (i)}
                        <div class="flex items-start gap-3 p-2.5">
                            {@render bar("size-8 shrink-0")}
                            <div class="flex-1 space-y-2 pt-0.5">
                                {@render bar("h-3 w-4/5")}
                                {@render bar("h-2.5 w-1/2")}
                            </div>
                        </div>
                    {/each}
                </div>
            </Card>
        </div>
    </div>

    <Card padding="lg">
        <Heading level={2} size="sm">Tasks</Heading>
        <div class="mt-4 space-y-2">
            {#each [0, 1, 2, 3] as i (i)}
                <div
                    class="space-y-2 rounded-[4px] border border-l-4 border-outline bg-surface-raised px-4 py-3"
                >
                    {@render bar("h-3.5 w-48")}
                    {@render bar("h-2.5 w-96 max-w-full")}
                </div>
            {/each}
        </div>
    </Card>
</PageContainer>
