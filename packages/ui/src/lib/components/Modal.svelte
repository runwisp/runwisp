<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Snippet } from "svelte";
    import { X } from "@lucide/svelte";

    interface Props {
        open?: boolean;
        title?: string;
        description?: string | undefined;
        size?: "sm" | "md" | "lg" | "xl" | "full";
        closable?: boolean;
        onClose?: () => void;
        header?: Snippet;
        footer?: Snippet;
        children?: Snippet;
        class?: string;
    }

    import { portal } from "../actions/portal.js";

    let {
        open = $bindable(false),
        title,
        description,
        size = "md",
        closable = true,
        onClose,
        header,
        footer,
        children,
        class: className = "",
    }: Props = $props();

    const sizeClasses: Record<string, string> = {
        sm: "max-w-sm",
        md: "max-w-md",
        lg: "max-w-lg",
        xl: "max-w-xl",
        full: "max-w-4xl",
    };

    function handleClose() {
        open = false;
        onClose?.();
    }

    function handleKeydown(e: KeyboardEvent) {
        if (e.key === "Escape" && closable) {
            handleClose();
        }
    }
</script>

<svelte:window onkeydown={handleKeydown} />

{#if open}
    <div
        use:portal
        class="
			fixed inset-0
			z-50 flex items-center justify-center
			p-4
		"
    >
        {#if closable}
            <button
                type="button"
                class="absolute inset-0 z-0 bg-backdrop backdrop-blur-sm"
                aria-label="Close modal"
                tabindex="-1"
                onclick={handleClose}
            ></button>
        {:else}
            <div class="absolute inset-0 z-0 bg-backdrop backdrop-blur-sm"></div>
        {/if}

        <div
            class="
				relative z-10 w-full {sizeClasses[size]}
				flex max-h-[90vh] flex-col
				rounded-2xl bg-surface-overlay shadow-xl
				{className}
			"
            role="dialog"
            aria-modal="true"
            aria-labelledby={title ? "modal-title" : undefined}
        >
            <!-- Header -->
            {#if header}
                <div class="border-b border-outline-faint px-6 py-4">
                    {@render header()}
                </div>
            {:else if title || closable}
                <div
                    class="flex items-start justify-between gap-4 border-b border-outline-faint px-6 py-4"
                >
                    <div>
                        {#if title}
                            <h2 id="modal-title" class="text-lg font-semibold text-on-surface">
                                {title}
                            </h2>
                        {/if}
                        {#if description}
                            <p class="mt-1 text-sm text-on-surface-muted">{description}</p>
                        {/if}
                    </div>
                    {#if closable}
                        <button
                            onclick={handleClose}
                            class="-m-2 shrink-0 rounded-lg p-2 text-on-surface-faint transition-colors hover:bg-surface-sunken hover:text-on-surface-muted"
                            aria-label="Close"
                        >
                            <X size={20} />
                        </button>
                    {/if}
                </div>
            {/if}

            <!-- Body -->
            {#if children}
                <div class="flex-1 overflow-y-auto px-6 py-4">
                    {@render children()}
                </div>
            {/if}

            <!-- Footer -->
            {#if footer}
                <div
                    class="rounded-b-2xl border-t border-outline-faint bg-surface-sunken/50 px-6 py-4"
                >
                    {@render footer()}
                </div>
            {/if}
        </div>
    </div>
{/if}
