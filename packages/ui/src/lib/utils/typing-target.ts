// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

/** True when a keyboard event came from a field the user is typing into. */
export function isTypingTarget(target: EventTarget | null): boolean {
    return (
        target instanceof HTMLElement &&
        (target.tagName === "INPUT" || target.tagName === "TEXTAREA" || target.isContentEditable)
    );
}
