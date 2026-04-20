<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    let {
        data,
        color = "#6366f1",
        fillColor,
        height = 40,
        class: className = "",
    } = $props<{
        data: number[];
        color?: string;
        fillColor?: string;
        height?: number;
        class?: string;
    }>();

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

    let resolvedFill = $derived(fillColor ?? color + "18");
</script>

<svg
    viewBox="0 0 {viewBoxW} {height}"
    preserveAspectRatio="none"
    class="w-full {className}"
    style="height: {height}px"
>
    {#if data.length >= 2}
        <path d={fillD} fill={resolvedFill} />
        <path d={pathD} fill="none" stroke={color} stroke-width="1.5" stroke-linejoin="round" />
    {/if}
</svg>
