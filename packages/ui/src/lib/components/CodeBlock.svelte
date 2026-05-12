<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { Check, Copy } from "@lucide/svelte";

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

<div class="overflow-hidden rounded-xl border border-outline bg-surface-sunken {className}">
    {#if filename || language}
        <div
            class="flex items-center justify-between border-b border-outline-faint px-4 py-2 font-mono text-xs text-on-surface-muted"
        >
            <span>{filename ?? language}</span>
        </div>
    {/if}
    <div class="relative">
        <pre
            class="overflow-x-auto px-4 py-4 font-mono text-sm leading-relaxed text-on-surface"><code
                >{code}</code
            ></pre>
        <button
            type="button"
            onclick={handleCopy}
            class="absolute top-2 right-2 inline-flex items-center gap-1.5 rounded-md border border-outline bg-surface-raised px-2 py-1 text-xs font-medium text-on-surface-muted shadow-sm transition-colors hover:border-outline-hover hover:text-on-surface"
            aria-label={copied ? "Copied" : "Copy to clipboard"}
        >
            {#if copied}
                <Check size={14} class="text-success-surface" />
                <span>Copied</span>
            {:else}
                <Copy size={14} />
                <span>Copy</span>
            {/if}
        </button>
    </div>
</div>
