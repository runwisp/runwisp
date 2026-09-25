// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// Trailing-edge debounce. Returns a wrapper that defers `fn` until `ms` have
// elapsed since the last call; `.cancel()` drops a pending call (used when an
// external write must win immediately, e.g. Reset clearing a debounced field).
export function debounce<A extends unknown[]>(
    fn: (...args: A) => void,
    ms: number,
): ((...args: A) => void) & { cancel: () => void } {
    let timer: ReturnType<typeof setTimeout> | undefined;
    const wrapped = (...args: A) => {
        clearTimeout(timer);
        timer = setTimeout(() => {
            fn(...args);
        }, ms);
    };
    wrapped.cancel = () => {
        clearTimeout(timer);
    };
    return wrapped;
}
