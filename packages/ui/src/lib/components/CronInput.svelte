<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { Info } from "@lucide/svelte";
    import Input from "./Input.svelte";
    import Modal from "./Modal.svelte";
    import cronstrue from "cronstrue";
    import { CronExpressionParser } from "cron-parser";

    let {
        value = $bindable(),
        disabled = false,
        timezone,
    }: {
        value: string;
        disabled?: boolean;
        /** IANA zone the schedule runs in; the next-run preview is shown in it. */
        timezone?: string;
    } = $props();

    let examplesModalOpen = $state(false);

    const cronExamples = [
        { cron: "* * * * *", description: "Every minute" },
        { cron: "0 * * * *", description: "Every hour" },
        { cron: "0 0 * * *", description: "Every day at midnight" },
        { cron: "0 0 * * 0", description: "Every Sunday at midnight" },
        { cron: "0 0 1 * *", description: "First day of every month" },
        { cron: "0 9 * * 1-5", description: "Weekdays at 9 AM" },
        { cron: "*/15 * * * *", description: "Every 15 minutes" },
        { cron: "0 */6 * * *", description: "Every 6 hours" },
        { cron: "30 4 1,15 * *", description: "1st and 15th at 4:30 AM" },
        { cron: "0 0 * * 0,6", description: "Weekends at midnight" },
    ];

    function selectExample(cron: string) {
        value = cron;
        examplesModalOpen = false;
    }

    // "Mon, Nov 2, 09:00 AM UTC (Mon 10:00 AM your time)" in the schedule's zone,
    // with the browser-local time appended when the zones differ.
    function formatNext(date: Date): string {
        if (!timezone) return date.toLocaleString();
        const zoned = date.toLocaleString(undefined, {
            weekday: "short",
            month: "short",
            day: "numeric",
            hour: "2-digit",
            minute: "2-digit",
            timeZone: timezone,
            timeZoneName: "short",
        });
        if (timezone === Intl.DateTimeFormat().resolvedOptions().timeZone) return zoned;
        const local = date.toLocaleString(undefined, {
            weekday: "short",
            hour: "2-digit",
            minute: "2-digit",
        });
        return `${zoned} (${local} your time)`;
    }

    let explanation = $derived.by(() => {
        if (!value || value.trim() === "") {
            return { text: "Empty schedule", next: undefined, isValid: false };
        }
        try {
            // cronstrue throws on invalid cron if throwExceptionOnParseError is true,
            // but we want to catch it to show "Invalid cron expression".
            const text = cronstrue.toString(value, { throwExceptionOnParseError: true });

            let next: string | undefined;
            try {
                const date = CronExpressionParser.parse(value, timezone ? { tz: timezone } : {})
                    .next()
                    .toDate();
                next = formatNext(date);
            } catch {
                // If parser fails, we consider it invalid or at least can't show next run
            }

            return { text, next, isValid: true };
        } catch {
            return { text: "Invalid cron expression", next: undefined, isValid: false };
        }
    });

    let isError = $derived(!disabled && value && value.trim() !== "" && !explanation.isValid);
</script>

<div class="space-y-2">
    <div class="flex items-center justify-between">
        <div class="font-mono text-xs font-medium text-on-surface-muted">Cron Schedule</div>
        <button
            type="button"
            class="flex items-center gap-1 font-mono text-xs text-primary hover:text-primary-hover"
            onclick={() => (examplesModalOpen = true)}
        >
            Examples <Info size={12} />
        </button>
    </div>
    <div class="flex gap-4">
        <div class="flex-1">
            <Input
                placeholder="* * * * *"
                bind:value
                {disabled}
                class="font-mono {isError ? 'border-danger-surface!' : ''}"
            />
        </div>
    </div>
    <div
        class="flex items-start gap-3 rounded-[4px] border p-3 {isError
            ? 'border-danger-soft-border bg-danger-soft'
            : 'border-outline bg-surface-sunken'}"
    >
        <Info size={18} class="mt-0.5 {isError ? 'text-danger-surface' : 'text-primary'}" />
        <div>
            <div
                class="font-sans text-sm font-medium {isError
                    ? 'text-danger-soft-text'
                    : 'text-on-surface'}"
            >
                {explanation.text}
            </div>
            {#if explanation.next}
                <div class="mt-0.5 font-sans text-xs text-on-surface-muted">
                    Next run: <span class="font-mono font-medium text-on-surface"
                        >{explanation.next}</span
                    >
                </div>
            {/if}
        </div>
    </div>
</div>

<Modal title="Cron Examples" size="lg" bind:open={examplesModalOpen}>
    <div class="space-y-2">
        <p class="text-sm text-on-surface-muted">
            Click an example to use it, or use the format: minute hour day-of-month month
            day-of-week
        </p>
        <div class="mt-4 max-h-100 space-y-1 overflow-y-auto">
            {#each cronExamples as example (example.cron)}
                <button
                    type="button"
                    class="flex w-full items-center justify-between rounded-[3px] px-3 py-2 text-left hover:bg-surface-sunken"
                    onclick={() => selectExample(example.cron)}
                >
                    <code class="font-mono text-sm text-on-surface">{example.cron}</code>
                    <span class="text-sm text-on-surface-muted">{example.description}</span>
                </button>
            {/each}
        </div>
    </div>
</Modal>
