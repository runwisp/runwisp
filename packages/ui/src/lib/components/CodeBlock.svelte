<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    interface Props {
        code: string;
        language?: string;
        filename?: string;
        class?: string;
    }

    let { code, language, filename, class: className = "" }: Props = $props();

    let copied = $state(false);
    let resetTimer = $state<ReturnType<typeof setTimeout> | undefined>(undefined);

    async function writeClipboard(): Promise<void> {
        if (typeof navigator === "undefined" || !navigator.clipboard) return;
        try {
            await navigator.clipboard.writeText(code);
            copied = true;
            if (resetTimer) clearTimeout(resetTimer);
            resetTimer = setTimeout(() => {
                copied = false;
            }, 2000);
        } catch {
            copied = false;
        }
    }

    function handleCopy(): void {
        void writeClipboard();
    }
</script>

<div
    class="overflow-hidden rounded-[4px] border border-term-line bg-term text-term-text {className}"
>
    {#if filename || language}
        <div
            class="flex items-center justify-between border-b border-term-line-2 bg-term-2 px-4 py-2 font-mono text-xs text-term-muted"
        >
            <span>{filename ?? language}</span>
        </div>
    {/if}
    <div class="relative">
        <pre
            class="overflow-x-auto px-4 py-4 font-mono text-sm leading-relaxed text-term-text"><code
                >{code}</code
            ></pre>
        <button
            type="button"
            onclick={handleCopy}
            class="absolute top-2 right-2 inline-flex items-center rounded-[3px] border border-term-line-2 bg-term-2 px-2 py-1 font-mono text-xs text-term-muted hover:border-term-teal hover:text-term-teal"
            aria-label={copied ? "Copied" : "Copy to clipboard"}
        >
            {copied ? "[copied]" : "[copy]"}
        </button>
    </div>
</div>
