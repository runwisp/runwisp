<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script module>
    import { defineMeta } from "@storybook/addon-svelte-csf";
    import LogConsole from "$lib/components/LogConsole.svelte";
    import LogConsoleDemo from "./LogConsoleDemo.svelte";

    const { Story } = defineMeta({
        title: "Dashboard/LogConsole",
        component: LogConsole,
        tags: ["autodocs"],
        parameters: {
            backgrounds: { default: "dark" },
        },
    });

    const ESC = "\x1b[";

    const SAMPLE_LOG = [
        `${ESC}90m2026-06-05T03:00:00+02:00${ESC}0m ${ESC}1mbackup-database${ESC}0m starting`,
        `${ESC}36m[info]${ESC}0m connecting to postgres://localhost:5432/app`,
        `${ESC}36m[info]${ESC}0m dumping schema ${ESC}1mpublic${ESC}0m (12 tables)`,
        `  ${ESC}32m✓${ESC}0m users            ${ESC}90m(184,221 rows, 48 MB)${ESC}0m`,
        `  ${ESC}32m✓${ESC}0m sessions         ${ESC}90m(902,114 rows, 210 MB)${ESC}0m`,
        `  ${ESC}32m✓${ESC}0m invoices         ${ESC}90m(45,002 rows, 12 MB)${ESC}0m`,
        `${ESC}33m[warn]${ESC}0m table ${ESC}1maudit_log${ESC}0m exceeds 1 GB — consider partitioning`,
        `  ${ESC}32m✓${ESC}0m audit_log        ${ESC}90m(7,212,330 rows, 1.2 GB)${ESC}0m`,
        `${ESC}31m[error]${ESC}0m checksum mismatch on chunk 7, retrying ${ESC}90m(attempt 1/3)${ESC}0m`,
        `  ${ESC}32m✓${ESC}0m retry succeeded`,
        `${ESC}36m[info]${ESC}0m compressing with ${ESC}35mzstd -19${ESC}0m`,
        `${ESC}36m[info]${ESC}0m uploading to ${ESC}34ms3://backups/app/2026-06-05.tar.zst${ESC}0m`,
        `${ESC}32m[done]${ESC}0m backup completed in ${ESC}1m42.7s${ESC}0m ${ESC}90m(exit 0)${ESC}0m`,
    ];

    const PALETTE_DEMO = [
        "Standard foreground (30–37):",
        `${ESC}30mblack${ESC}0m ${ESC}31mred${ESC}0m ${ESC}32mgreen${ESC}0m ${ESC}33myellow${ESC}0m ${ESC}34mblue${ESC}0m ${ESC}35mmagenta${ESC}0m ${ESC}36mcyan${ESC}0m ${ESC}37mwhite${ESC}0m`,
        "",
        "Bright foreground (90–97):",
        `${ESC}90mblack${ESC}0m ${ESC}91mred${ESC}0m ${ESC}92mgreen${ESC}0m ${ESC}93myellow${ESC}0m ${ESC}94mblue${ESC}0m ${ESC}95mmagenta${ESC}0m ${ESC}96mcyan${ESC}0m ${ESC}97mwhite${ESC}0m`,
        "",
        "Bold + colour:",
        `${ESC}1;31mbold red${ESC}0m ${ESC}1;32mbold green${ESC}0m ${ESC}1;34mbold blue${ESC}0m ${ESC}1;36mbold cyan${ESC}0m`,
        "",
        "Backgrounds (41–47):",
        `${ESC}41m red ${ESC}0m ${ESC}42m green ${ESC}0m ${ESC}43m yellow ${ESC}0m ${ESC}44m blue ${ESC}0m ${ESC}45m magenta ${ESC}0m ${ESC}46m cyan ${ESC}0m`,
        "",
        "Underline + colour:",
        `${ESC}4;36munderlined cyan link${ESC}0m and ${ESC}4;33munderlined yellow${ESC}0m`,
    ];

    const STREAM_TICKS = [
        `${ESC}36m[info]${ESC}0m processed batch ${ESC}1m#42${ESC}0m ${ESC}90m(1,024 records)${ESC}0m`,
        `${ESC}32m✓${ESC}0m health check passed`,
        `${ESC}33m[warn]${ESC}0m slow query detected ${ESC}90m(812ms)${ESC}0m`,
        `${ESC}36m[info]${ESC}0m flushing write buffer`,
    ];
</script>

<Story name="Sample Run Log" asChild>
    <LogConsoleDemo lines={SAMPLE_LOG} />
</Story>

<Story name="ANSI Palette" asChild>
    <LogConsoleDemo lines={PALETTE_DEMO} />
</Story>

<Story name="Streaming" asChild>
    <LogConsoleDemo lines={SAMPLE_LOG.slice(0, 5)} finished={false} streamLines={STREAM_TICKS} />
</Story>

<Story name="Truncated" asChild>
    <LogConsoleDemo lines={SAMPLE_LOG.slice(6)} firstAvailableLine={1500} />
</Story>
