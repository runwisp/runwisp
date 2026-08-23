// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

const FOCUSABLE_SELECTOR = [
    "a[href]",
    "button:not([disabled])",
    "input:not([disabled])",
    "select:not([disabled])",
    "textarea:not([disabled])",
    '[tabindex]:not([tabindex="-1"])',
].join(",");

function isHTMLElement(value: unknown): value is HTMLElement {
    return value instanceof HTMLElement;
}

/** All focusable, visible descendants of `container`, in DOM (tab) order. */
export function getFocusableElements(container: HTMLElement): HTMLElement[] {
    return Array.from(container.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR)).filter(
        (el) => el.offsetParent !== null || el === document.activeElement,
    );
}

/**
 * Pure decision function: given a keydown event, the currently focusable
 * elements (in order), and whatever currently has focus, returns the element
 * that Tab/Shift+Tab should wrap focus to in order to keep it trapped inside
 * the set — or `null` if the key doesn't need handling.
 *
 * Framework- and DOM-free on purpose (elements are compared by identity only)
 * so this can be unit tested without a browser or jsdom.
 */
export function resolveTrapFocusTarget<T>(
    event: Pick<KeyboardEvent, "key" | "shiftKey">,
    focusable: readonly T[],
    active: T | null,
): T | null {
    if (event.key !== "Tab" || focusable.length === 0) return null;
    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    if (first === undefined || last === undefined) return null;

    if (event.shiftKey && active === first) return last;
    if (!event.shiftKey && active === last) return first;
    return null;
}

export interface TrapFocusOptions {
    /**
     * Move focus to the first focusable descendant (or the container itself)
     * as soon as the trap is created. Defaults to `true`; set to `false` when
     * the caller already manages initial focus (e.g. a searchable combobox
     * that focuses its own search input).
     */
    autoFocus?: boolean;
}

/**
 * Svelte action that keeps keyboard focus inside `node` while it's mounted:
 * Tab from the last focusable descendant wraps to the first (and Shift+Tab
 * from the first wraps to the last), and focus is restored to whatever was
 * focused beforehand once the trap is torn down — unless a click on some
 * other real element already moved focus there, in which case that's left
 * alone.
 */
export function trapFocus(node: HTMLElement, options: TrapFocusOptions = {}) {
    const autoFocus = options.autoFocus ?? true;
    const previouslyFocused = isHTMLElement(document.activeElement) ? document.activeElement : null;

    if (autoFocus) {
        // Deferred a tick: `node` may still be moved by a sibling `use:portal`
        // action (appendChild-ing it to <body>) after this action runs, and
        // relocating a focused element resets focus to <body>. Waiting a
        // microtask lets any such move finish first, so the focus target
        // ends up wherever the element actually settles.
        queueMicrotask(() => {
            const target = getFocusableElements(node)[0] ?? node;
            if (target === node && !node.hasAttribute("tabindex")) {
                node.tabIndex = -1;
            }
            target.focus();
        });
    }

    function handleKeydown(e: KeyboardEvent) {
        const active = isHTMLElement(document.activeElement) ? document.activeElement : null;
        const target = resolveTrapFocusTarget(e, getFocusableElements(node), active);
        if (target) {
            e.preventDefault();
            target.focus();
        }
    }

    node.addEventListener("keydown", handleKeydown);

    return {
        destroy() {
            node.removeEventListener("keydown", handleKeydown);
            // By the time an action's destroy() runs, `node` (and whatever
            // inside it held focus) has already been detached — removing a
            // focused element resets focus to <body> as a browser default, so
            // that can't be told apart from an explicit close (Escape, item
            // activation) by checking `node.contains(activeElement)` here.
            // What *can* still be told apart: a deliberate outside click
            // already moved focus to its own (still-attached) target before
            // this runs, so activeElement is a real element other than
            // <body>. Only restore focus when that's not the case.
            const active = document.activeElement;
            const focusWasReset = !isHTMLElement(active) || active === document.body;
            if (focusWasReset && previouslyFocused && document.contains(previouslyFocused)) {
                previouslyFocused.focus();
            }
        },
    };
}
