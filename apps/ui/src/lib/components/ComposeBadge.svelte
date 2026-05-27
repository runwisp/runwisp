<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { Badge } from "@runwisp/ui";

    interface Props {
        file: string;
        service?: string;
        projectName?: string;
    }

    let { file, service, projectName }: Props = $props();

    const tooltip = $derived.by(() => {
        const parts = [file];
        if (service) {
            parts.push(`service: ${service}`);
        }
        if (projectName) {
            parts.push(`project: ${projectName}`);
        }
        return parts.join(" · ");
    });
</script>

<span title={tooltip}>
    <Badge variant="info" size="sm" outline>compose</Badge>
</span>
