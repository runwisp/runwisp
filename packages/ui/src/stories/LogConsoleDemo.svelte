<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<!-- Storybook-only harness: LogConsole is seeded by its parent calling
     onStream(), so this wrapper feeds it a canned LogEvent on mount and,
     optionally, keeps appending lines to demo live streaming. -->
<script lang="ts">
    import { onMount } from "svelte";
    import LogConsole from "$lib/components/LogConsole.svelte";

    interface Props {
        lines: string[];
        finished?: boolean;
        firstAvailableLine?: number;
        streamLines?: string[];
        height?: string;
    }

    let {
        lines,
        finished = true,
        firstAvailableLine = 0,
        streamLines = [],
        height = "24rem",
    }: Props = $props();

    let logConsole = $state<LogConsole | null>(null);

    function toEvent(slice: Record<number, string>, total: number): void {
        logConsole?.onStream({
            lines: slice,
            sizeLines: total,
            sizeBytes: Object.values(slice).reduce((n, l) => n + l.length + 1, 0),
            finished,
            firstAvailableLine,
        });
    }

    onMount(() => {
        const slice: Record<number, string> = {};
        lines.forEach((text, i) => {
            slice[firstAvailableLine + i] = text;
        });
        toEvent(slice, firstAvailableLine + lines.length);

        if (finished || streamLines.length === 0) return;

        let next = firstAvailableLine + lines.length;
        const timer = setInterval(() => {
            const text = streamLines[(next - lines.length) % streamLines.length] ?? "";
            toEvent({ [next]: text }, next + 1);
            next += 1;
        }, 700);
        return () => clearInterval(timer);
    });
</script>

<div class="overflow-hidden rounded-xl border border-outline" style="height: {height};">
    <LogConsole bind:this={logConsole} />
</div>
