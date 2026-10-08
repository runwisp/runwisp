// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// showModal() puts the dialog in the top layer and makes the page behind it
// inert. The dialog stays open while mounted; the caller's `open` flag drives
// (un)mounting.
export function showModal(node: HTMLDialogElement) {
    node.showModal();
}

interface ModalDialogOptions {
    closable: () => boolean;
    isOpen: () => boolean;
    close: () => void;
}

// The slice of a pointer/click event the handlers read.
interface Pressed {
    target: EventTarget | null;
    currentTarget: EventTarget | null;
}

/**
 * Event handlers to spread on a `<dialog use:showModal>`: Escape and a press on
 * the bare dialog (its backdrop area) close it when closable.
 */
export function modalDialogHandlers({ closable, isOpen, close }: ModalDialogOptions) {
    // Only a press that starts and ends on the backdrop closes the dialog, so a
    // text selection dragged out of the panel doesn't.
    let pressedBackdrop = false;
    return {
        oncancel(e: { preventDefault(): void }) {
            e.preventDefault();
            if (closable()) close();
        },
        // Chrome ignores preventDefault() on a repeated Escape and closes anyway;
        // reopen so a non-closable dialog stays up while mounted.
        onclose(e: { currentTarget: { showModal(): void } }) {
            if (isOpen()) e.currentTarget.showModal();
        },
        onpointerdown(e: Pressed) {
            pressedBackdrop = e.target === e.currentTarget;
        },
        onclick(e: Pressed) {
            if (closable() && pressedBackdrop && e.target === e.currentTarget) close();
        },
    };
}
