<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import Button from "@runwisp/ui/components/Button.svelte";
    import Modal from "@runwisp/ui/components/Modal.svelte";
    import type { Component } from "svelte";

    interface Props {
        open: boolean;
        title: string;
        description: string;
        confirmLabel: string;
        variant: "primary" | "danger";
        icon: Component<{ size?: number }>;
        onConfirm: () => void;
    }

    let {
        open = $bindable(false),
        title,
        description,
        confirmLabel,
        variant,
        icon: Icon,
        onConfirm,
    }: Props = $props();

    function confirm() {
        open = false;
        onConfirm();
    }
</script>

<Modal bind:open {title} {description} size="sm">
    {#snippet footer()}
        <div class="flex justify-end gap-2">
            <Button variant="secondary" size="sm" onclick={() => (open = false)}>Cancel</Button>
            <Button {variant} size="sm" onclick={confirm}>
                {#snippet icon()}<Icon size={16} />{/snippet}
                {confirmLabel}
            </Button>
        </div>
    {/snippet}
</Modal>
