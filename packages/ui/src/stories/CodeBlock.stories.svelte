<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script module>
    import { defineMeta } from "@storybook/addon-svelte-csf";
    import CodeBlock from "$lib/components/CodeBlock.svelte";

    const helloTask = '[tasks.hello]\ncron = "*/5 * * * *"\nrun  = "echo hello"';
    const backupTask = '[tasks.backup]\ncron = "0 3 * * *"\nkeep_runs = 60';

    const { Story } = defineMeta({
        title: "Data Display/CodeBlock",
        component: CodeBlock,
        tags: ["autodocs"],
        argTypes: {
            variant: { control: "select", options: ["terminal", "surface"] },
            size: { control: "select", options: ["sm", "md"] },
        },
    });
</script>

<Story
    name="Default"
    args={{
        code: "export RUNWISP_STATION_TOKEN=rw_xxx\nexport RUNWISP_STATION_URL=https://app.runwisp.com\nrunwisp station",
    }}
/>

<Story
    name="Filename"
    args={{ code: "FROM alpine:3.20\nRUN apk add --no-cache curl", filename: "Dockerfile" }}
/>

<Story
    name="Shell prompt"
    args={{ code: "curl -fsSL https://get.runwisp.com/install.sh | bash", prompt: "$" }}
/>

<Story name="Surface variant" asChild>
    <CodeBlock variant="surface" size="sm" copyable={false} code={helloTask} />
</Story>

<!-- Rich children render the markup; `code` keeps the copied text plain. -->
<Story name="Highlighted children" asChild>
    <!-- No whitespace between the line spans: inside <pre> it would render as blank lines. -->
    <CodeBlock filename="runwisp.toml" code={backupTask}
        ><span class="block text-[var(--rw-syn-fn)]">[tasks.backup]</span><span class="block"
            ><span class="text-[var(--rw-syn-kw)]">cron</span> =
            <span class="text-[var(--rw-syn-str)]">"0 3 * * *"</span></span
        ><span class="block"
            ><span class="text-[var(--rw-syn-kw)]">keep_runs</span> =
            <span class="text-[var(--rw-syn-num)]">60</span></span
        ></CodeBlock
    >
</Story>
