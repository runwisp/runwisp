<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Component, Snippet } from "svelte";
    import Modal from "./Modal.svelte";
    import Button from "./Button.svelte";
    import type { ButtonVariant } from "./button-styles.js";

    interface Props {
        open?: boolean;
        title: string;
        description?: string | undefined;
        size?: "sm" | "md" | "lg" | "xl" | "full";
        confirmLabel?: string;
        cancelLabel?: string;
        confirmVariant?: ButtonVariant;
        confirmDisabled?: boolean;
        confirmIcon?: Component<{ size?: number }>;
        /** May be async: the dialog shows a spinner and closes once it resolves.
         *  If it throws, the dialog stays open so the caller can surface the error. */
        onConfirm?: () => void | Promise<void>;
        onCancel?: () => void;
        children?: Snippet;
        /** Replaces the default Cancel / Confirm buttons. */
        footer?: Snippet;
        class?: string;
    }

    let {
        open = $bindable(false),
        title,
        description,
        size = "sm",
        confirmLabel = "Confirm",
        cancelLabel = "Cancel",
        confirmVariant = "danger",
        confirmDisabled = false,
        confirmIcon: ConfirmIcon,
        onConfirm,
        onCancel,
        children,
        footer: footerProp,
        class: className = "",
    }: Props = $props();

    let confirming = $state(false);

    async function handleConfirm() {
        if (confirming) return;
        confirming = true;
        try {
            await onConfirm?.();
            open = false;
        } finally {
            confirming = false;
        }
    }

    function handleCancel() {
        if (confirming) return;
        open = false;
        onCancel?.();
    }
</script>

{#snippet confirmIconSnippet()}
    {#if ConfirmIcon}<ConfirmIcon size={16} />{/if}
{/snippet}

<Modal
    bind:open
    {title}
    {description}
    {size}
    closable={!confirming}
    onClose={handleCancel}
    {children}
    class={className}
>
    {#snippet footer()}
        {#if footerProp}
            {@render footerProp()}
        {:else}
            <div class="flex items-center justify-end gap-3">
                <Button variant="secondary" onclick={handleCancel} disabled={confirming}>
                    {cancelLabel}
                </Button>
                <Button
                    variant={confirmVariant}
                    loading={confirming}
                    disabled={confirming || confirmDisabled}
                    onclick={() => void handleConfirm()}
                    icon={ConfirmIcon ? confirmIconSnippet : undefined}
                >
                    {confirmLabel}
                </Button>
            </div>
        {/if}
    {/snippet}
</Modal>
