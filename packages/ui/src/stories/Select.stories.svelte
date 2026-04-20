<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script module>
    import { defineMeta } from "@storybook/addon-svelte-csf";
    import Select from "$lib/components/Select.svelte";
    import { Globe, Clock, Zap, Cloud, Cpu, Database } from "@lucide/svelte";

    const { Story } = defineMeta({
        title: "Forms/Select",
        component: Select,
        tags: ["autodocs"],
        argTypes: {
            size: {
                control: "select",
                options: ["sm", "md", "lg"],
            },
            searchable: {
                control: "boolean",
            },
            disabled: {
                control: "boolean",
            },
        },
    });

    const simpleOptions = [
        { value: "option1", label: "Option 1" },
        { value: "option2", label: "Option 2" },
        { value: "option3", label: "Option 3" },
    ];

    const richOptions = [
        {
            value: "load-balanced",
            label: "Load Balanced",
            description: "Distribute tasks evenly across available runners based on load.",
            icon: Zap,
        },
        {
            value: "round-robin",
            label: "Round Robin",
            description: "Cycle through runners sequentially.",
            icon: Clock,
        },
        {
            value: "geodistributed",
            label: "Geo-Distributed",
            description: "Route to the nearest available daemon.",
            icon: Globe,
        },
        {
            value: "fan-out",
            label: "Fan Out",
            description: "Execute on all connected runners simultaneously.",
            icon: Database,
            disabled: true,
        },
    ];

    const groupedOptions = [
        { value: "us-east-1", label: "US East (N. Virginia)", group: "Americas", icon: Cloud },
        { value: "us-west-1", label: "US West (N. California)", group: "Americas", icon: Cloud },
        { value: "eu-west-1", label: "EU (Ireland)", group: "Europe", icon: Globe },
        { value: "eu-central-1", label: "EU (Frankfurt)", group: "Europe", icon: Globe },
        {
            value: "ap-southeast-1",
            label: "Asia Pacific (Singapore)",
            group: "Asia Pacific",
            icon: Globe,
        },
        {
            value: "ap-northeast-1",
            label: "Asia Pacific (Tokyo)",
            group: "Asia Pacific",
            icon: Globe,
        },
        { value: "local", label: "Local Machine", group: "Development", icon: Cpu },
    ];
</script>

<Story
    name="Default"
    args={{
        options: simpleOptions,
        placeholder: "Select an option...",
    }}
/>

<Story
    name="With Label & Hint"
    args={{
        label: "Deployment Strategy",
        options: richOptions,
        placeholder: "Choose strategy...",
        hint: "Select how tasks are distributed to runners.",
    }}
/>

<Story
    name="Searchable"
    args={{
        label: "Region",
        options: groupedOptions,
        searchable: true,
        placeholder: "Search regions...",
        description: "Filter by region name.",
    }}
/>

<Story
    name="With Error"
    args={{
        label: "Required Field",
        options: simpleOptions,
        error: "This field is required.",
        placeholder: "Select...",
    }}
/>

<Story name="Sizes" asChild>
    <div class="max-w-sm space-y-6">
        <Select size="sm" label="Small Size" options={simpleOptions} placeholder="Small" />
        <Select
            size="md"
            label="Medium Size"
            options={richOptions}
            placeholder="Medium with Icons"
        />
        <Select
            size="lg"
            label="Large Size"
            options={groupedOptions}
            placeholder="Large with Groups"
            searchable={true}
        />
    </div>
</Story>

<Story
    name="Rich Content"
    args={{
        label: "Complex Selection",
        options: richOptions,
        placeholder: "Select with icons & descriptions...",
        class: "max-w-md",
    }}
/>

<Story
    name="Disabled"
    args={{
        label: "Disabled Input",
        options: simpleOptions,
        value: "option1",
        disabled: true,
    }}
/>
