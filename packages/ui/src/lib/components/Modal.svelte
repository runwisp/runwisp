<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Snippet } from "svelte";
    import { X } from "@lucide/svelte";
    import Heading from "./Heading.svelte";

    interface Props {
        open?: boolean;
        title?: string;
        description?: string | undefined;
        size?: "sm" | "md" | "lg" | "xl" | "full";
        closable?: boolean;
        onClose?: () => void;
        header?: Snippet;
        footer?: Snippet;
        // Explicitly nullable so callers can pass `children={cond ? snip : undefined}`
        // to suppress the padded body band entirely when there's nothing to show.
        children?: Snippet | undefined;
        class?: string;
    }

    import { trapFocus } from "../actions/focusTrap.js";
    import { modalDialogHandlers, showModal } from "../actions/modal-dialog.js";

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

    const titleId = $props.id();

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

    const dialogHandlers = modalDialogHandlers({
        closable: () => closable,
        isOpen: () => open,
        close: handleClose,
    });
</script>

{#if open}
    <dialog
        use:showModal
        {...dialogHandlers}
        aria-labelledby={title ? titleId : undefined}
        class="
			fixed inset-0 m-0 flex h-full max-h-none w-full max-w-none
			items-center justify-center border-0 bg-transparent p-4
			text-inherit backdrop:bg-transparent
		"
    >
        <div
            class="pointer-events-none absolute inset-0 z-0 bg-backdrop backdrop-blur-sm"
            aria-hidden="true"
        ></div>

        <div
            use:trapFocus
            class="
				relative z-10 w-full {sizeClasses[size]}
				flex max-h-[90vh] flex-col
				rounded-[4px] border border-outline bg-surface-overlay shadow-lg
				{className}
			"
        >
            {#if header}
                <div class="border-b border-outline px-6 py-4">
                    {@render header()}
                </div>
            {:else if title || closable}
                <div
                    class="flex items-start justify-between gap-4 border-b border-outline px-6 py-4"
                >
                    <div>
                        {#if title}
                            <Heading level={2} size="lg" id={titleId}>{title}</Heading>
                        {/if}
                        {#if description}
                            <p class="mt-1 text-sm text-on-surface-muted">{description}</p>
                        {/if}
                    </div>
                    {#if closable}
                        <button
                            onclick={handleClose}
                            class="-m-2 shrink-0 rounded-[3px] p-2 text-on-surface-faint hover:bg-surface-sunken hover:text-on-surface-muted"
                            aria-label="Close"
                        >
                            <X size={20} />
                        </button>
                    {/if}
                </div>
            {/if}

            {#if children}
                <div class="flex-1 overflow-y-auto px-6 py-4">
                    {@render children()}
                </div>
            {/if}

            {#if footer}
                <div class="rounded-b-[4px] border-t border-outline bg-surface-sunken px-6 py-4">
                    {@render footer()}
                </div>
            {/if}
        </div>
    </dialog>
{/if}
