// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Run, RunSelector } from "@runwisp/common";

// The subset of the toast options this module inspects. @runwisp/ui doesn't
// export its ToastOptions type, so describe the captured-argument shape locally
// — this keeps reading the recorded Undo action type-safe.
interface CapturedToastOptions {
    duration?: number;
    action?: { label: string; onClick: () => void };
}

// Hoisted so the (hoisted) vi.mock factory below can close over them. Typed with
// the toast signature so reading captured call args stays type-safe — and
// referencing these plain fns in assertions avoids the unbound-method lint that
// `toast.success` (a class method) would trip.
const toastMocks = vi.hoisted(() => ({
    success: vi.fn<(message: string, opts?: CapturedToastOptions) => string>(),
    error: vi.fn<(message: string, opts?: CapturedToastOptions) => string>(),
    info: vi.fn<(message: string, opts?: CapturedToastOptions) => string>(),
}));

// Replace the API layer and the toast/error helpers so the orchestration here is
// tested in isolation: the API calls are spies, and toast side effects are
// captured rather than rendered.
vi.mock("$lib/api", () => ({
    runsApi: {
        bulkDelete: vi.fn(),
        bulkRestore: vi.fn(),
        bulkCancel: vi.fn(),
        bulkRerun: vi.fn(),
    },
}));
vi.mock("@runwisp/ui", () => ({
    toast: toastMocks,
    extractErrorMessage: (err: unknown, fallback: string): string =>
        err instanceof Error ? err.message : fallback,
}));

import { runsApi } from "$lib/api";
import { createRunActions } from "./run-actions";

function makeRun(id: string, overrides: Partial<Run> = {}): Run {
    return {
        id,
        task_name: "backup-db",
        created_at: "2026-06-22T12:00:00.000Z",
        status: "ended",
        end_reason: "success",
        triggered_by: "api",
        exit_code: 0,
        instance_index: 0,
        retry_attempt: 0,
        ...overrides,
    };
}

function setup(initial: Run[] = []) {
    let items = [...initial];
    const onOptimisticRemove = vi.fn((ids: string[]) => {
        items = items.filter((r) => !ids.includes(r.id));
    });
    const onOptimisticRestore = vi.fn((runs: Run[]) => {
        items = [...items, ...runs];
    });
    const onRemoved = vi.fn();
    const actions = createRunActions({
        getItems: () => items,
        onOptimisticRemove,
        onOptimisticRestore,
        onRemoved,
    });
    return { actions, onOptimisticRemove, onOptimisticRestore, onRemoved };
}

/** The Undo handler attached to the most recent success toast, if any. */
function lastUndoHandler(): (() => void) | undefined {
    return toastMocks.success.mock.calls.at(-1)?.[1]?.action?.onClick;
}

const idsSelector = (ids: string[]): RunSelector => ({ match_all: false, ids });

beforeEach(() => {
    vi.clearAllMocks();
});

describe("handleBulkDelete", () => {
    it("does nothing when no runs are affected", async () => {
        const { actions } = setup();
        await actions.handleBulkDelete(idsSelector([]), []);
        expect(runsApi.bulkDelete).not.toHaveBeenCalled();
        expect(toastMocks.success).not.toHaveBeenCalled();
    });

    it("removes optimistically, reports removed ids, and toasts the count", async () => {
        const run = makeRun("a");
        const { actions, onOptimisticRemove, onRemoved } = setup([run]);
        vi.mocked(runsApi.bulkDelete).mockResolvedValue(1);

        await actions.handleBulkDelete(idsSelector(["a"]), [run]);

        expect(onOptimisticRemove).toHaveBeenCalledWith(["a"]);
        expect(onRemoved).toHaveBeenCalledWith(new Set(["a"]));
        expect(runsApi.bulkDelete).toHaveBeenCalledWith(idsSelector(["a"]));
        expect(toastMocks.success).toHaveBeenCalledWith("Run deleted", expect.anything());
    });

    it("pluralises the toast when more than one run is deleted", async () => {
        const runs = [makeRun("a"), makeRun("b")];
        const { actions } = setup(runs);
        vi.mocked(runsApi.bulkDelete).mockResolvedValue(2);

        await actions.handleBulkDelete(idsSelector(["a", "b"]), runs);

        expect(toastMocks.success).toHaveBeenCalledWith("2 runs deleted", expect.anything());
    });

    it("rolls back and toasts an error when the delete fails", async () => {
        const run = makeRun("a");
        const { actions, onOptimisticRestore } = setup([run]);
        vi.mocked(runsApi.bulkDelete).mockRejectedValue(new Error("boom"));

        await actions.handleBulkDelete(idsSelector(["a"]), [run]);

        expect(onOptimisticRestore).toHaveBeenCalledWith([run]);
        expect(toastMocks.error).toHaveBeenCalledWith("boom");
    });

    it("undo restores the snapshot via bulkRestore", async () => {
        const run = makeRun("a");
        const { actions, onOptimisticRestore } = setup([run]);
        vi.mocked(runsApi.bulkDelete).mockResolvedValue(1);
        vi.mocked(runsApi.bulkRestore).mockResolvedValue(1);

        await actions.handleBulkDelete(idsSelector(["a"]), [run]);
        lastUndoHandler()?.();

        await vi.waitFor(() => {
            expect(runsApi.bulkRestore).toHaveBeenCalledWith(idsSelector(["a"]));
        });
        expect(onOptimisticRestore).toHaveBeenLastCalledWith([run]);
    });

    it("undo toasts an error when the restore fails", async () => {
        const run = makeRun("a");
        const { actions } = setup([run]);
        vi.mocked(runsApi.bulkDelete).mockResolvedValue(1);
        vi.mocked(runsApi.bulkRestore).mockRejectedValue(new Error("no restore"));

        await actions.handleBulkDelete(idsSelector(["a"]), [run]);
        lastUndoHandler()?.();

        await vi.waitFor(() => {
            expect(toastMocks.error).toHaveBeenCalledWith("no restore");
        });
    });
});

describe("handleBulkCancel", () => {
    it("does nothing when no runs are affected", async () => {
        const { actions } = setup();
        await actions.handleBulkCancel(idsSelector([]), []);
        expect(runsApi.bulkCancel).not.toHaveBeenCalled();
    });

    it("toasts the singular count", async () => {
        const run = makeRun("a", { status: "running" });
        const { actions } = setup([run]);
        vi.mocked(runsApi.bulkCancel).mockResolvedValue(1);

        await actions.handleBulkCancel(idsSelector(["a"]), [run]);

        expect(toastMocks.success).toHaveBeenCalledWith("Cancelled 1 run");
    });

    it("toasts the plural count", async () => {
        const runs = [makeRun("a", { status: "running" }), makeRun("b", { status: "running" })];
        const { actions } = setup(runs);
        vi.mocked(runsApi.bulkCancel).mockResolvedValue(2);

        await actions.handleBulkCancel(idsSelector(["a", "b"]), runs);

        expect(toastMocks.success).toHaveBeenCalledWith("Cancelled 2 runs");
    });

    it("toasts an error when cancel fails", async () => {
        const run = makeRun("a", { status: "running" });
        const { actions } = setup([run]);
        vi.mocked(runsApi.bulkCancel).mockRejectedValue(new Error("cancel failed"));

        await actions.handleBulkCancel(idsSelector(["a"]), [run]);

        expect(toastMocks.error).toHaveBeenCalledWith("cancel failed");
    });
});

describe("handleBulkRerun", () => {
    it("toasts the triggered count and offers undo", async () => {
        const run = makeRun("a");
        const { actions } = setup([run]);
        vi.mocked(runsApi.bulkRerun).mockResolvedValue({
            triggered: [{ task_name: "backup-db", run_id: "r1" }],
        });

        await actions.handleBulkRerun(idsSelector(["a"]), [run]);

        expect(toastMocks.success).toHaveBeenCalledWith("Triggered 1 task", expect.anything());
    });

    it("pluralises the triggered label", async () => {
        const { actions } = setup();
        vi.mocked(runsApi.bulkRerun).mockResolvedValue({
            triggered: [
                { task_name: "a", run_id: "r1" },
                { task_name: "b", run_id: "r2" },
            ],
        });

        await actions.handleBulkRerun(idsSelector(["a", "b"]), []);

        expect(toastMocks.success).toHaveBeenCalledWith("Triggered 2 tasks", expect.anything());
    });

    it("toasts an error when nothing could be re-run", async () => {
        const { actions } = setup();
        vi.mocked(runsApi.bulkRerun).mockResolvedValue({ triggered: [] });

        await actions.handleBulkRerun(idsSelector(["a"]), []);

        expect(toastMocks.error).toHaveBeenCalledWith("Could not re-run any of the selected tasks");
        expect(toastMocks.success).not.toHaveBeenCalled();
    });

    it("toasts an error when the re-run request fails", async () => {
        const { actions } = setup();
        vi.mocked(runsApi.bulkRerun).mockRejectedValue(new Error("rerun failed"));

        await actions.handleBulkRerun(idsSelector(["a"]), []);

        expect(toastMocks.error).toHaveBeenCalledWith("rerun failed");
    });

    it("undo cancels then deletes the freshly triggered runs", async () => {
        const { actions } = setup();
        vi.mocked(runsApi.bulkRerun).mockResolvedValue({
            triggered: [{ task_name: "backup-db", run_id: "r1" }],
        });
        vi.mocked(runsApi.bulkCancel).mockResolvedValue(1);
        vi.mocked(runsApi.bulkDelete).mockResolvedValue(1);

        await actions.handleBulkRerun(idsSelector(["a"]), []);
        lastUndoHandler()?.();

        await vi.waitFor(() => {
            expect(runsApi.bulkDelete).toHaveBeenCalledWith(idsSelector(["r1"]));
        });
        expect(runsApi.bulkCancel).toHaveBeenCalledWith(idsSelector(["r1"]));
        expect(toastMocks.info).toHaveBeenCalledWith("Re-run undone");
    });

    it("undo swallows a cancel failure but still reports a delete failure", async () => {
        const { actions } = setup();
        vi.mocked(runsApi.bulkRerun).mockResolvedValue({
            triggered: [{ task_name: "backup-db", run_id: "r1" }],
        });
        vi.mocked(runsApi.bulkCancel).mockRejectedValue(new Error("already done"));
        vi.mocked(runsApi.bulkDelete).mockRejectedValue(new Error("undo delete failed"));

        await actions.handleBulkRerun(idsSelector(["a"]), []);
        lastUndoHandler()?.();

        await vi.waitFor(() => {
            expect(toastMocks.error).toHaveBeenCalledWith("undo delete failed");
        });
    });
});

describe("deleteSingle", () => {
    it("deletes the matching run", async () => {
        const run = makeRun("a");
        const { actions } = setup([run]);
        vi.mocked(runsApi.bulkDelete).mockResolvedValue(1);

        actions.deleteSingle("a");

        await vi.waitFor(() => {
            expect(runsApi.bulkDelete).toHaveBeenCalledWith(idsSelector(["a"]));
        });
    });

    it("does nothing when the id is not in the current list", () => {
        const { actions } = setup([makeRun("a")]);
        actions.deleteSingle("missing");
        expect(runsApi.bulkDelete).not.toHaveBeenCalled();
    });
});
