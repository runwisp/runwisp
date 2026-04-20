<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { Info } from "@lucide/svelte";
    import Input from "./Input.svelte";
    import Modal from "./Modal.svelte";
    import cronstrue from "cronstrue";
    import { CronExpressionParser } from "cron-parser";

    let { value = $bindable(), disabled = false } = $props<{
        value: string;
        disabled?: boolean;
    }>();

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

    let explanation = $derived.by(() => {
        if (!value || value.trim() === "") {
            return { text: "Empty schedule", next: undefined, isValid: false };
        }
        try {
            // cronstrue throws on invalid cron if throwExceptionOnParseError is true,
            // but we want to catch it to show "Invalid cron expression".
            const text = cronstrue.toString(value, { throwExceptionOnParseError: true });

            // Calculate next run
            let next: Date | undefined;
            try {
                const interval = CronExpressionParser.parse(value);
                next = interval.next().toDate();
            } catch {
                // If parser fails, we consider it invalid or at least can't show next run
            }

            return { text, next, isValid: true };
        } catch {
            return { text: "Invalid cron expression", next: undefined, isValid: false };
        }
    });

    // Determine state for styling
    let isError = $derived(!disabled && value && value.trim() !== "" && !explanation.isValid);
</script>

<div class="space-y-2">
    <div class="flex items-center justify-between">
        <div class="text-sm font-medium text-on-surface-muted">Cron Schedule</div>
        <button
            type="button"
            class="flex items-center gap-1 text-xs text-primary hover:text-primary-hover"
            onclick={() => (examplesModalOpen = true)}
        >
            Examples <Info size={12} />
        </button>
    </div>
    <div class="flex gap-4">
        <div class="flex-1">
            <!-- Pass connection-status-like classes effectively by using the component props or class -->
            <Input
                placeholder="* * * * *"
                bind:value
                {disabled}
                class="font-mono {isError
                    ? '!focus:border-danger-500 !focus:ring-danger-500 border-danger-300!'
                    : ''}"
            />
        </div>
    </div>
    <!-- Live Feedback -->
    <div
        class="flex items-start gap-3 rounded-md border p-3 transition-colors duration-200
        {isError ? 'border-danger-200 bg-danger-soft' : 'border-outline bg-surface-sunken'}"
    >
        <Info size={18} class="mt-0.5 {isError ? 'text-danger-surface' : 'text-primary'}" />
        <div>
            <div
                class="text-sm font-medium {isError ? 'text-danger-soft-text' : 'text-on-surface'}"
            >
                {explanation.text}
            </div>
            {#if explanation.next}
                <div class="mt-0.5 text-xs text-on-surface-muted">
                    Next run: <span class="font-medium text-on-surface"
                        >{explanation.next.toLocaleString()}</span
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
                    class="flex w-full items-center justify-between rounded-lg px-3 py-2 text-left transition-colors hover:bg-surface-sunken"
                    onclick={() => selectExample(example.cron)}
                >
                    <code class="font-mono text-sm text-on-surface">{example.cron}</code>
                    <span class="text-sm text-on-surface-muted">{example.description}</span>
                </button>
            {/each}
        </div>
    </div>
</Modal>
