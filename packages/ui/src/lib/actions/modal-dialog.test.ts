// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it, vi } from "vitest";
import { modalDialogHandlers } from "./modal-dialog.js";

function setup(closable: boolean, open = true) {
    const close = vi.fn();
    const handlers = modalDialogHandlers({ closable: () => closable, isOpen: () => open, close });
    const dialog = new EventTarget();
    return { close, handlers, dialog, showModal: vi.fn() };
}

describe("modalDialogHandlers", () => {
    it("closes on Escape only when closable, and always cancels the native close", () => {
        const closable = setup(true);
        const event = { preventDefault: vi.fn() };
        closable.handlers.oncancel(event);
        expect(event.preventDefault).toHaveBeenCalled();
        expect(closable.close).toHaveBeenCalledOnce();

        const locked = setup(false);
        locked.handlers.oncancel(event);
        expect(locked.close).not.toHaveBeenCalled();
    });

    it("closes on a press that starts and ends on the backdrop", () => {
        const { handlers, close, dialog } = setup(true);
        handlers.onpointerdown({ target: new EventTarget(), currentTarget: dialog });
        handlers.onclick({ target: dialog, currentTarget: dialog });
        expect(close).not.toHaveBeenCalled();

        handlers.onpointerdown({ target: dialog, currentTarget: dialog });
        handlers.onclick({ target: dialog, currentTarget: dialog });
        expect(close).toHaveBeenCalledOnce();
    });

    it("reopens a dialog the browser closed while it is still mounted", () => {
        const mounted = setup(false, true);
        mounted.handlers.onclose({ currentTarget: mounted });
        expect(mounted.showModal).toHaveBeenCalledOnce();

        const gone = setup(false, false);
        gone.handlers.onclose({ currentTarget: gone });
        expect(gone.showModal).not.toHaveBeenCalled();
    });
});
