<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { Badge } from "@runwisp/ui";

    interface Props {
        name: string;
        source: "staged" | "cron";
        /** Absolute path of the crontab or staging file the definition came from. */
        sourceFile?: string;
    }

    let { name, source, sourceFile }: Props = $props();

    // A cron-sourced task's definition lives in a crontab RunWisp reads but never
    // writes, so naming the exact file is the whole point of the badge: the
    // operator edits it with `crontab -e` and picks the change up with
    // `runwisp reload`. A staged task always lives in the one staging file.
    const origin = $derived(
        source === "cron"
            ? `the crontab ${sourceFile ? sourceFile : "RunWisp read it from"}`
            : "runwisp.d/imported.toml by an import",
    );

    // Display-only: the badge says where the task came from and names the CLI
    // that graduates it. Nothing here writes TOML — only the CLI does.
    const tooltip = $derived(
        `Defined in ${origin}, not native TOML yet. ` +
            `It runs like any other task; \`runwisp promote ${name}\` moves it into runwisp.toml.`,
    );
</script>

<span title={tooltip}>
    <Badge size="sm" outline>{source}</Badge>
</span>
