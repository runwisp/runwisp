<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    // The task's configuration on demand, in runwisp.toml key names so the
    // operator knows what to edit. Read-only: TOML is the source of truth.
    import type { Task } from "@runwisp/common";
    import { Drawer } from "@runwisp/ui";
    import { taskDetailSections } from "$lib/utils/task-details";

    let { task, open = $bindable(false) }: { task: Task; open: boolean } = $props();

    const sections = $derived(taskDetailSections(task));
</script>

<Drawer bind:open side="right" size="lg" title="Details">
    <div class="flex flex-col gap-5" data-testid="task-details">
        <p class="text-sm text-on-surface-muted">
            Read-only, as written in runwisp.toml. Edit the file, then run
            <code class="font-mono text-xs">runwisp reload</code>. Keys left at their defaults are
            hidden.
        </p>
        {#each sections as section (section.title)}
            <section>
                <h3
                    class="mb-2 font-mono text-2xs font-medium tracking-[0.16em] text-on-surface-faint uppercase"
                >
                    {section.title}
                </h3>
                {#if section.command}
                    <pre
                        class="overflow-x-auto rounded-[3px] bg-surface-sunken px-3 py-2 font-mono text-xs whitespace-pre text-on-surface"
                        data-testid="task-command">{section.command}</pre>
                {/if}
                {#if section.rows.length > 0}
                    <dl
                        class="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1.5 font-mono text-xs {section.command
                            ? 'mt-2'
                            : ''}"
                    >
                        {#each section.rows as row (row.key)}
                            <dt class="text-on-surface-muted">{row.key}</dt>
                            <dd class="break-all text-on-surface">
                                {row.value}
                                {#if row.note}
                                    <span class="ml-1 font-sans text-on-surface-faint"
                                        >{row.note}</span
                                    >
                                {/if}
                            </dd>
                        {/each}
                    </dl>
                {/if}
            </section>
        {/each}
    </div>
</Drawer>
