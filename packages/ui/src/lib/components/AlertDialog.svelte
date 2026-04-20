<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import Modal from "./Modal.svelte";
    import Button from "./Button.svelte";

    type AlertVariant = "danger" | "warning" | "info";

    interface Props {
        open?: boolean;
        title: string;
        description?: string | undefined;
        confirmLabel?: string;
        cancelLabel?: string;
        variant?: AlertVariant;
        onConfirm?: () => void;
        onCancel?: () => void;
        loading?: boolean;
        class?: string;
    }

    let {
        open = $bindable(false),
        title,
        description,
        confirmLabel = "Confirm",
        cancelLabel = "Cancel",
        variant = "danger",
        onConfirm,
        onCancel,
        loading = false,
        class: className = "",
    }: Props = $props();

    const confirmVariantMap: Record<AlertVariant, "danger" | "primary"> = {
        danger: "danger",
        warning: "primary",
        info: "primary",
    };

    function handleCancel() {
        open = false;
        onCancel?.();
    }

    function handleConfirm() {
        onConfirm?.();
    }
</script>

<Modal bind:open {title} {description} size="sm" closable onClose={handleCancel} class={className}>
    {#snippet footer()}
        <div class="flex items-center justify-end gap-3">
            <Button variant="secondary" onclick={handleCancel} disabled={loading}>
                {cancelLabel}
            </Button>
            <Button variant={confirmVariantMap[variant]} onclick={handleConfirm} {loading}>
                {confirmLabel}
            </Button>
        </div>
    {/snippet}
</Modal>
