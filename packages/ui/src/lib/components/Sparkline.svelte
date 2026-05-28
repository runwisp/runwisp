<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    interface Props {
        data: number[];
        color?: string;
        fillColor?: string;
        fillOpacity?: number;
        height?: number;
        class?: string;
    }

    // `currentColor` by default so the caller can theme us with a Tailwind
    // text-* class — that's the only way to flip on .dark without burning a
    // hardcoded hex in here.
    let {
        data,
        color = "currentColor",
        fillColor,
        fillOpacity = 0.12,
        height = 40,
        class: className = "",
    }: Props = $props();

    const viewBoxW = 200;

    let pathD = $derived.by(() => {
        if (data.length < 2) return "";

        const max = 100;
        const step = viewBoxW / (data.length - 1);

        return data
            .map((v: number, i: number) => {
                const x = i * step;
                const y = height - (v / max) * height;
                return `${i === 0 ? "M" : "L"}${x.toFixed(1)},${y.toFixed(1)}`;
            })
            .join(" ");
    });

    let fillD = $derived.by(() => {
        if (data.length < 2 || !pathD) return "";
        const step = viewBoxW / (data.length - 1);
        const lastX = (data.length - 1) * step;
        return `${pathD} L${lastX.toFixed(1)},${height} L0,${height} Z`;
    });

    // Use `fill-opacity` rather than appending "18" to a hex — that trick
    // breaks for `currentColor` / `oklch()` / `var(...)` values.
    let resolvedFill = $derived(fillColor ?? color);
    let resolvedFillOpacity = $derived(fillColor === undefined ? fillOpacity : 1);
</script>

<svg
    viewBox="0 0 {viewBoxW} {height}"
    preserveAspectRatio="none"
    class="w-full {className}"
    style="height: {height}px"
>
    {#if data.length >= 2}
        <path d={fillD} fill={resolvedFill} fill-opacity={resolvedFillOpacity} />
        <path d={pathD} fill="none" stroke={color} stroke-width="1.5" stroke-linejoin="round" />
    {/if}
</svg>
