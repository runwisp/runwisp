<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { ansiLineToHtml } from "./ansi.js";

    let { text }: { text: string } = $props();
</script>

<!-- ansiLineToHtml sanitises its input, which is what makes @html safe here. -->
<!-- eslint-disable-next-line svelte/no-at-html-tags -->
<span class="whitespace-pre">{@html ansiLineToHtml(text)}</span>
