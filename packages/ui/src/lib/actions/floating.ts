// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import type { Placement } from "@floating-ui/dom";
import { autoUpdate, computePosition, flip, offset, shift } from "@floating-ui/dom";

export interface FloatingOptions {
    reference: HTMLElement | null;
    placement: Placement;
    offset: number;
    padding: number;
    /** Size the floating element to the reference's width (e.g. a select menu). */
    matchWidth?: boolean;
    /** When true the element is left unpositioned (e.g. shown as a bottom sheet). */
    disabled?: boolean;
    /** Called with the final placement after flip(), e.g. to set a transform origin. */
    onPlace?: (placement: Placement) => void;
}

/**
 * Svelte action that keeps `node` positioned against `options.reference`
 * while mounted, re-computing on scroll, resize and layout changes.
 */
export function floating(node: HTMLElement, options: FloatingOptions) {
    let stop: (() => void) | null = null;

    function start(opts: FloatingOptions) {
        stop?.();
        stop = null;
        const reference = opts.reference;
        if (opts.disabled === true || !reference) {
            node.style.removeProperty("left");
            node.style.removeProperty("top");
            node.style.removeProperty("position");
            return;
        }
        if (opts.matchWidth === true) node.style.width = `${String(reference.offsetWidth)}px`;
        // A position computed after stop() must not land on a re-purposed node.
        let live = true;
        const cleanup = autoUpdate(reference, node, () => {
            void computePosition(reference, node, {
                placement: opts.placement,
                middleware: [offset(opts.offset), flip(), shift({ padding: opts.padding })],
            }).then(({ x, y, placement }) => {
                if (!live) return;
                Object.assign(node.style, {
                    left: `${String(x)}px`,
                    top: `${String(y)}px`,
                    position: "absolute",
                });
                opts.onPlace?.(placement);
            });
        });
        stop = () => {
            live = false;
            cleanup();
        };
    }

    start(options);
    return {
        update: start,
        destroy() {
            stop?.();
        },
    };
}

/** Whether a click landed outside every given element (null entries are ignored). */
export function isOutsideClick(e: MouseEvent, ...elements: (HTMLElement | null)[]): boolean {
    const path = e.composedPath();
    return !elements.some((el) => el !== null && path.includes(el));
}
