<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script module>
    import { defineMeta } from "@storybook/addon-svelte-csf";
    import FilterBar from "$lib/components/FilterBar.svelte";
    import { seedValues } from "$lib/components/filter-spec.js";

    const { Story } = defineMeta({
        title: "Data/FilterBar",
        component: FilterBar,
        tags: ["autodocs"],
    });

    /** @type {import("$lib/components/filter-spec.js").FilterField} */
    const searchField = {
        type: "search",
        key: "q",
        fields: ["name"],
        placeholder: "Search tasks...",
    };

    /** @type {import("$lib/components/filter-spec.js").FilterField[]} */
    const fields = [
        searchField,
        {
            type: "select",
            key: "status",
            label: "Status",
            primary: true,
            options: [
                { value: "all", label: "All" },
                { value: "success", label: "Success" },
                { value: "failed", label: "Failed" },
                { value: "running", label: "Running" },
            ],
        },
        { type: "number", key: "exitCode", label: "Exit code" },
        { type: "daterange", key: "lastRun", label: "Last run" },
    ];
</script>

<script>
    let values = $state(seedValues(fields));
    let preset = $state(seedValues(fields, { status: "failed", exitCode: "1" }));
</script>

<Story name="Default" asChild>
    <div class="flex flex-col gap-4">
        <FilterBar {fields} bind:values />
        <pre class="font-mono text-xs text-on-surface-muted">{JSON.stringify(values, null, 2)}</pre>
    </div>
</Story>

<Story name="Pre-filled (URL restore)" asChild>
    <FilterBar {fields} bind:values={preset} />
</Story>

<Story name="Search Only" asChild>
    <FilterBar fields={[searchField]} values={seedValues([searchField])} />
</Story>
