<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Snippet } from "svelte";
    import { CopyFeedback } from "../utils/clipboard.svelte.js";

    type CodeBlockVariant = "terminal" | "surface";
    type CodeBlockSize = "sm" | "md";

    interface Props {
        /** Plain source. Rendered unless `children` is set, and always what the copy button copies. */
        code?: string;
        /** Rich or highlighted markup. Without `code`, the copy button copies its rendered text. */
        children?: Snippet;
        language?: string;
        filename?: string;
        /** Shell prompt before the first line, e.g. "$". Never copied or selected. */
        prompt?: string;
        /** `terminal` stays dark in both themes; `surface` follows the page theme for in-page snippets. */
        variant?: CodeBlockVariant;
        size?: CodeBlockSize;
        copyable?: boolean;
        class?: string;
    }

    let {
        code,
        children,
        language,
        filename,
        prompt,
        variant = "terminal",
        size = "md",
        copyable = true,
        class: className = "",
    }: Props = $props();

    const feedback = new CopyFeedback();
    let codeEl = $state<HTMLElement | undefined>(undefined);

    function handleCopy(): void {
        void feedback.copy(code ?? codeEl?.textContent ?? "");
    }

    const variantClasses: Record<
        CodeBlockVariant,
        { frame: string; header: string; prompt: string; button: string }
    > = {
        terminal: {
            frame: "border-term-line bg-term text-term-text",
            header: "border-term-line-2 bg-term-2 text-term-muted",
            prompt: "text-term-teal",
            button: "border-term-line-2 bg-term-2 text-term-muted hover:border-term-teal hover:text-term-teal",
        },
        surface: {
            frame: "border-outline bg-surface-sunken text-on-surface-muted",
            header: "border-outline bg-surface text-on-surface-faint",
            prompt: "text-on-surface-faint",
            button: "border-outline bg-surface-raised text-on-surface-faint hover:border-outline-hover hover:text-primary",
        },
    };

    const sizeClasses: Record<CodeBlockSize, string> = {
        sm: "px-4 py-3 text-xs",
        md: "px-4 py-4 text-sm leading-relaxed",
    };

    const tone = $derived(variantClasses[variant]);
    const hasHeader = $derived(Boolean(filename || language));
</script>

{#snippet copyButton(extra: string)}
    <button
        type="button"
        onclick={handleCopy}
        class="inline-flex items-center rounded-[3px] border px-2 py-1 font-mono text-xs {tone.button} {extra}"
        aria-label={feedback.copied ? "Copied" : "Copy to clipboard"}
    >
        {feedback.copied ? "[copied]" : "[copy]"}
    </button>
{/snippet}

<div class="overflow-hidden rounded-[4px] border {tone.frame} {className}">
    {#if hasHeader}
        <div
            class="flex items-center justify-between gap-3 border-b px-4 py-2 font-mono text-xs {tone.header}"
        >
            <span>{filename ?? language}</span>
            {#if copyable}
                {@render copyButton("-my-1")}
            {/if}
        </div>
    {/if}
    <div class="relative">
        <pre
            class="overflow-x-auto font-mono {sizeClasses[size]} {copyable && !hasHeader
                ? 'pr-24'
                : ''}">{#if prompt}<span
                    class="mr-[1ch] select-none {tone.prompt}"
                    aria-hidden="true">{prompt}</span
                >{/if}<code bind:this={codeEl}
                >{#if children}{@render children()}{:else}{code}{/if}</code
            ></pre>
        {#if copyable && !hasHeader}
            {@render copyButton("absolute top-2 right-2")}
        {/if}
    </div>
</div>
