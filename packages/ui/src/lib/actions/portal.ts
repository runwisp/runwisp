// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// Inside a modal <dialog> the rest of the page is inert and below the top
// layer, so content opened from it (a menu, a popover) is moved into the
// dialog instead of <body>.
export function portal(node: HTMLElement) {
    const host = node.parentElement?.closest("dialog[open]") ?? document.body;
    host.appendChild(node);
    return {
        destroy() {
            node.remove();
        },
    };
}
