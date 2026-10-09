// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

/**
 * Folds keys away, in `order`, until `fits()` says the row fits. Returns the
 * keys it folded. Fixed order, so a control is always in the same place: in
 * the row, or in its overflow menu.
 */
export function foldUntilFits(
    order: readonly string[],
    fits: () => boolean,
    fold: (key: string) => void,
): string[] {
    const folded: string[] = [];
    for (const key of order) {
        if (fits()) break;
        fold(key);
        folded.push(key);
    }
    return folded;
}

export interface FoldToFitOptions {
    /** Fold keys, first to go first. */
    order: readonly string[];
    /** Called with the folded keys whenever they change, to fill a ⋯ menu. */
    onfold?: (folded: string[]) => void;
}

/**
 * Keeps a one-line row from overflowing by folding parts of it away. Markup:
 * `[data-fold=k]` hides when `k` folds, `[data-fold-alt=k]` (render it with
 * `hidden`) shows instead, a shorter version. `[data-fold-shrink]` parts
 * normally truncate, so they stop shrinking while the row is measured.
 * Re-fits when the row resizes or its content changes.
 */
export function foldToFit(row: HTMLElement, options: FoldToFitOptions) {
    let opts = options;
    let reported = "";
    let frame = 0;

    const all = (selector: string) => Array.from(row.querySelectorAll<HTMLElement>(selector));
    const setFolded = (key: string, folded: boolean) => {
        for (const el of all(`[data-fold="${key}"]`)) el.hidden = folded;
        for (const el of all(`[data-fold-alt="${key}"]`)) el.hidden = !folded;
    };

    function fit() {
        frame = 0;
        for (const key of opts.order) setFolded(key, false);
        const shrinking = all("[data-fold-shrink]");
        for (const el of shrinking) el.style.flexShrink = "0";
        const folded = foldUntilFits(
            opts.order,
            () => row.scrollWidth <= row.clientWidth,
            (key) => {
                setFolded(key, true);
            },
        );
        for (const el of shrinking) el.style.flexShrink = "";
        const key = folded.join(" ");
        if (key !== reported) {
            reported = key;
            opts.onfold?.(folded);
        }
    }

    const schedule = () => {
        if (frame === 0) frame = requestAnimationFrame(fit);
    };
    const resize = new ResizeObserver(schedule);
    resize.observe(row);
    // Not attributes: fitting toggles `hidden`, which would loop.
    const content = new MutationObserver(schedule);
    content.observe(row, { childList: true, subtree: true, characterData: true });
    fit();

    return {
        update(next: FoldToFitOptions) {
            opts = next;
            schedule();
        },
        destroy() {
            cancelAnimationFrame(frame);
            resize.disconnect();
            content.disconnect();
        },
    };
}
