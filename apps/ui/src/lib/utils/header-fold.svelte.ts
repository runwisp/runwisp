// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * Folds the task page's header out of the log's way, driven by the reader's
 * own scrolling in the log or the run list:
 *   scroll FOLD_DISTANCE down without reversing = fold
 *   scroll FOLD_DISTANCE up without reversing = unfold
 *   reach the bottom = fold right away; reach the top = unfold right away
 *   a log short enough to need no scrollbar (with the header unfolded) = unfold,
 *   and no scrolling (the run list's included) folds it
 * Only the native scroll event measures distance, so wheel, touchpad, touch,
 * keys and the scrollbar all work alike. Scrolls the page makes itself
 * (opening a run at its end, following a live log, the list easing to a new
 * run) don't count.
 */

/** px. Tune on real hardware. */
export const FOLD_DISTANCE = 240;
/** How long the fold animates. */
export const FOLD_MS = 180;
// The fold resizes the log; scroll events in that window are its own.
const SETTLE_MS = FOLD_MS + 20;
// A scroll counts as the reader's only this soon after their wheel, touch,
// key or scrollbar press, or after the last scroll that counted (so touchpad
// and touch momentum keep counting).
const INTENT_MS = 250;
const SCROLL_KEYS = new Set(["ArrowUp", "ArrowDown", "PageUp", "PageDown", "Home", "End", " "]);

export interface ScrollStep {
    delta: number;
    atTop: boolean;
    atBottom: boolean;
}

/**
 * One counted scroll step: the run of same-direction distance so far, and
 * whether to fold (true), unfold (false) or wait (undefined).
 */
export function foldStep(run: number, step: ScrollStep): { run: number; fold?: boolean } {
    if (step.atTop) return { run: 0, fold: false };
    if (step.atBottom) return { run: 0, fold: true };
    const next = Math.sign(step.delta) === Math.sign(run) ? run + step.delta : step.delta;
    if (Math.abs(next) >= FOLD_DISTANCE) return { run: 0, fold: next > 0 };
    return { run: next };
}

/**
 * Whether a log fits without scrolling once the header is unfolded. Decided
 * for the unfolded header, or folding could make it fit and flip it back.
 */
export function fitsUnfolded(
    contentHeight: number,
    clientHeight: number,
    folded: boolean,
    stripDelta: number,
): boolean {
    return contentHeight <= (folded ? clientHeight - stripDelta : clientHeight);
}

type Scroller = Pick<Element, "scrollTop" | "scrollHeight" | "clientHeight">;
type LogBox = Pick<HTMLElement, "dataset" | "clientHeight">;
interface Sample {
    top: number;
    height: number;
    client: number;
}

export class HeaderFold {
    folded = $state(false);
    /** How much taller the unfolded header is than the folded one, px. */
    stripDelta = 0;
    /** The log console, marked by its data-content-height. Set by attach. */
    findLog: () => LogBox | null = () => null;

    readonly #now: () => number;
    #run = 0;
    #settleUntil = 0;
    #intentUntil = 0;
    #samples = new WeakMap<Scroller, Sample>();

    constructor(now: () => number = () => performance.now()) {
        this.#now = now;
    }

    set(folded: boolean): void {
        if (this.folded === folded) return;
        this.folded = folded;
        this.#run = 0;
        this.#settleUntil = this.#now() + SETTLE_MS;
    }

    toggle(): void {
        this.set(!this.folded);
    }

    /** The reader just pressed something that scrolls. */
    intent(): void {
        this.#intentUntil = this.#now() + INTENT_MS;
    }

    /** A scroll event from `el` (the log, or the run list). */
    scrolled(el: Scroller): void {
        const prev = this.#samples.get(el);
        const cur = { top: el.scrollTop, height: el.scrollHeight, client: el.clientHeight };
        this.#samples.set(el, cur);
        const now = this.#now();
        if (now < this.#settleUntil || now > this.#intentUntil) return;
        // Content or viewport changed size: appended lines, a re-pin, more
        // runs loaded, the fold itself. Not the reader.
        if (prev && (prev.height !== cur.height || prev.client !== cur.client)) return;
        const delta = prev ? cur.top - prev.top : 0;
        const atTop = cur.top <= 0;
        const atBottom = cur.height - cur.top - cur.client < 1;
        // Without a previous position only the ends can be judged.
        if (delta === 0 && !(prev === undefined && (atTop || atBottom))) return;
        this.#intentUntil = now + INTENT_MS;
        const step = foldStep(this.#run, { delta, atTop, atBottom });
        this.#run = step.run;
        if (step.fold === true && this.#logFits(this.findLog())) return;
        if (step.fold !== undefined) this.set(step.fold);
    }

    /** Unfolds when the log fits without scrolling. */
    checkShortLog(el: LogBox): void {
        if (!this.folded || this.#now() < this.#settleUntil) return;
        if (this.#logFits(el)) this.set(false);
    }

    #logFits(el: LogBox | null): boolean {
        const content = Number(el?.dataset.contentHeight);
        if (!el || !Number.isFinite(content)) return false;
        return fitsUnfolded(content, el.clientHeight, this.folded, this.stripDelta);
    }

    /**
     * Wires the fold to the page: scrolls anywhere inside `root` (capture
     * phase, scroll doesn't bubble), the reader's input, and the log's size.
     */
    attach(root: HTMLElement): () => void {
        const opts = { capture: true, passive: true };
        const onScroll = (e: Event) => {
            if (e.target instanceof Element) this.scrolled(e.target);
        };
        const onIntent = () => {
            this.intent();
        };
        const onKey = (e: KeyboardEvent) => {
            if (SCROLL_KEYS.has(e.key)) this.intent();
        };
        // A press on a scroller itself (not its content) is its scrollbar.
        const onPointer = (e: PointerEvent) => {
            if (e.target instanceof Element && e.target.scrollHeight > e.target.clientHeight) {
                this.intent();
            }
        };
        this.findLog = () => root.querySelector<HTMLElement>("[data-content-height]");
        let frame = 0;
        const checkSoon = () => {
            if (frame !== 0) return;
            frame = requestAnimationFrame(() => {
                frame = 0;
                const log = this.findLog();
                if (log) this.checkShortLog(log);
            });
        };
        root.addEventListener("scroll", onScroll, opts);
        root.addEventListener("wheel", onIntent, opts);
        root.addEventListener("touchmove", onIntent, opts);
        root.addEventListener("keydown", onKey, opts);
        root.addEventListener("pointerdown", onPointer, opts);
        const content = new MutationObserver(checkSoon);
        content.observe(root, {
            subtree: true,
            attributes: true,
            attributeFilter: ["data-content-height"],
        });
        const resize = new ResizeObserver(checkSoon);
        resize.observe(root);
        return () => {
            cancelAnimationFrame(frame);
            root.removeEventListener("scroll", onScroll, opts);
            root.removeEventListener("wheel", onIntent, opts);
            root.removeEventListener("touchmove", onIntent, opts);
            root.removeEventListener("keydown", onKey, opts);
            root.removeEventListener("pointerdown", onPointer, opts);
            content.disconnect();
            resize.disconnect();
        };
    }
}
