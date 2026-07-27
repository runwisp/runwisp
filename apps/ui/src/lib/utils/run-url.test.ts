// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("$app/navigation", () => ({ goto: vi.fn() }));

import { goto } from "$app/navigation";
import { navigateToRun } from "./run-url";

beforeEach(() => {
    vi.clearAllMocks();
});

describe("navigateToRun", () => {
    it("navigates to a new target with a history-preserving replaceState", () => {
        navigateToRun(new URL("https://host/runs"), "/runs/01ABC");

        expect(goto).toHaveBeenCalledTimes(1);
        expect(goto).toHaveBeenCalledWith("/runs/01ABC", {
            replaceState: true,
            keepFocus: true,
            noScroll: true,
        });
    });

    it("no-ops when already on the target path (absorbs the initial URL echo)", () => {
        navigateToRun(new URL("https://host/tasks/backup/01ABC"), "/tasks/backup/01ABC");

        expect(goto).not.toHaveBeenCalled();
    });
});
