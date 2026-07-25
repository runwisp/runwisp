<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { Badge } from "@runwisp/ui";

    interface Props {
        name: string;
    }

    let { name }: Props = $props();

    // Display-only: the badge says where the task came from and names the CLI
    // that graduates it. Nothing here writes TOML — only the CLI does.
    const tooltip = $derived(
        `Defined in runwisp.d/imported.toml by an import, not native TOML yet. ` +
            `It runs like any other task; \`runwisp promote ${name}\` moves it into runwisp.toml.`,
    );
</script>

<span title={tooltip}>
    <Badge size="sm" outline>staged</Badge>
</span>
