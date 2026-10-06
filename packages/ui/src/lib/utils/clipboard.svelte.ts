// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

const DEFAULT_RESET_MS = 2000;

// Copies text to the clipboard and reports whether it worked. navigator.clipboard
// only exists in secure contexts, so a daemon served over plain http on a LAN IP
// falls back to the legacy textarea + execCommand path.
export async function copyText(text: string): Promise<boolean> {
    if (typeof navigator !== "undefined" && "clipboard" in navigator) {
        try {
            await navigator.clipboard.writeText(text);
            return true;
        } catch {
            // Denied or blocked: try the legacy path below.
        }
    }
    return legacyCopy(text);
}

function legacyCopy(text: string): boolean {
    if (typeof document === "undefined") return false;
    const previousFocus = document.activeElement;
    const ta = document.createElement("textarea");
    ta.value = text;
    ta.readOnly = true;
    ta.style.position = "fixed";
    ta.style.opacity = "0";
    document.body.appendChild(ta);
    ta.select();
    let ok = false;
    try {
        // Deprecated, but still the only copy path outside a secure context.
        // eslint-disable-next-line @typescript-eslint/no-deprecated
        ok = document.execCommand("copy"); // NOSONAR
    } catch {
        ok = false;
    }
    ta.remove();
    if (previousFocus instanceof HTMLElement) previousFocus.focus();
    return ok;
}

// CopyFeedback drives a copy button's transient "copied" state. `key` tells
// several buttons sharing one instance apart (one per tab or snippet).
export class CopyFeedback {
    #key = $state<string | null>(null);
    #timer: ReturnType<typeof setTimeout> | undefined;
    readonly #resetMs: number;

    constructor(resetMs: number = DEFAULT_RESET_MS) {
        this.#resetMs = resetMs;
    }

    get copied(): boolean {
        return this.#key !== null;
    }

    isCopied(key: string): boolean {
        return this.#key === key;
    }

    async copy(text: string, key: string = ""): Promise<boolean> {
        const ok = await copyText(text);
        if (!ok) return false;
        clearTimeout(this.#timer);
        this.#key = key;
        this.#timer = setTimeout(() => {
            this.#key = null;
        }, this.#resetMs);
        return true;
    }
}
