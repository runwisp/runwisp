// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { connectionStore } from "./connection.svelte";
import { AuthRequiredError } from "$lib/api";

// ─── reportFetchError (exercises isConnectionError + formatError) ─────────────

describe("connectionStore.reportFetchError", () => {
    it("returns false and makes no state change for AuthRequiredError", () => {
        const before = connectionStore.status;
        const result = connectionStore.reportFetchError(new AuthRequiredError());
        expect(result).toBe(false);
        expect(connectionStore.status).toBe(before);
    });

    it("returns false for null (formatError returns null → isConnectionError false)", () => {
        expect(connectionStore.reportFetchError(null)).toBe(false);
    });

    it("returns false for a plain string that does not match the network-error regex", () => {
        expect(connectionStore.reportFetchError("HTTP 404 Not Found")).toBe(false);
    });

    it("returns false for a non-network Error", () => {
        expect(connectionStore.reportFetchError(new Error("Not Found"))).toBe(false);
    });

    it("returns false for a plain object whose JSON does not match the network regex", () => {
        expect(connectionStore.reportFetchError({ code: 503, reason: "overloaded" })).toBe(false);
    });

    it("returns true for a network TypeError and sets lastError", () => {
        // markDisconnected is called internally, which starts timers; markConnected
        // immediately after cancels them so the process can exit cleanly.
        const result = connectionStore.reportFetchError(new TypeError("Failed to fetch"));
        expect(result).toBe(true);
        connectionStore.markConnected(); // cancel retry / tick timers
    });
});

// ─── formatError branches via markDisconnected ────────────────────────────────

describe("connectionStore.markDisconnected formatError paths", () => {
    it("sets lastError to err.message when given an Error instance", () => {
        connectionStore.markDisconnected(new Error("disk full"));
        expect(connectionStore.lastError).toBe("disk full");
        connectionStore.markConnected();
    });

    it("sets lastError to the string itself when given a plain string", () => {
        connectionStore.markDisconnected("timed out");
        expect(connectionStore.lastError).toBe("timed out");
        connectionStore.markConnected();
    });

    it("sets lastError to null when given null", () => {
        connectionStore.markDisconnected(null);
        expect(connectionStore.lastError).toBeNull();
        connectionStore.markConnected();
    });

    it("sets lastError to null when given undefined", () => {
        connectionStore.markDisconnected(undefined);
        expect(connectionStore.lastError).toBeNull();
        connectionStore.markConnected();
    });
});

// ─── markConnected state transitions ─────────────────────────────────────────

describe("connectionStore.markConnected", () => {
    it("sets status to connected after a disconnect", () => {
        connectionStore.markDisconnected(new Error("err"));
        connectionStore.markConnected();
        expect(connectionStore.status).toBe("connected");
    });

    it("resets lastError to null after a disconnect with error", () => {
        connectionStore.markDisconnected(new Error("previous error"));
        connectionStore.markConnected();
        expect(connectionStore.lastError).toBeNull();
    });

    it("resets retryAttempts to 0", () => {
        connectionStore.markDisconnected(new Error("err"));
        connectionStore.markConnected();
        expect(connectionStore.retryAttempts).toBe(0);
    });
});

// ─── reportSourceUp / reportSourceDown multi-source semantics ────────────────

describe("connectionStore.reportSourceUp / reportSourceDown", () => {
    it("stays connected when one source goes down but another is still up", () => {
        connectionStore.reportSourceUp("a");
        connectionStore.reportSourceUp("b");
        connectionStore.reportSourceDown("a");
        expect(connectionStore.status).toBe("connected");
        // Cleanup
        connectionStore.reportSourceDown("b");
        connectionStore.markConnected();
    });

    it("transitions to disconnected only when all sources are down", () => {
        connectionStore.reportSourceUp("a");
        connectionStore.reportSourceUp("b");
        connectionStore.reportSourceDown("a");
        connectionStore.reportSourceDown("b", "boom");
        expect(connectionStore.status).toBe("disconnected");
        connectionStore.markConnected();
    });

    it("recovers to connected when any source comes back up", () => {
        connectionStore.reportSourceUp("a");
        connectionStore.reportSourceDown("a");
        expect(connectionStore.status).toBe("disconnected");
        connectionStore.reportSourceUp("b");
        expect(connectionStore.status).toBe("connected");
        connectionStore.reportSourceDown("b");
        connectionStore.markConnected();
    });
});

// ─── reportSourceStalled (browser connection-cap awareness) ──────────────────

describe("connectionStore.reportSourceStalled", () => {
    // The store is a singleton; drain every source touched (reportSourceDown
    // clears both up + stalled membership) then markConnected to cancel timers,
    // so each case starts from a clean slate.
    function drain(...ids: string[]) {
        for (const id of ids) connectionStore.reportSourceDown(id);
        connectionStore.markConnected();
    }

    it("enters 'stalled' — distinct from 'disconnected' — when a source stalls", () => {
        connectionStore.reportSourceStalled("a");
        expect(connectionStore.status).toBe("stalled");
        // A stall is not a network outage: no error text, no retry scheduled.
        expect(connectionStore.lastError).toBeNull();
        expect(connectionStore.nextRetryAt).toBeNull();
        drain("a");
    });

    it("stays connected while another source is still up", () => {
        connectionStore.reportSourceUp("a");
        connectionStore.reportSourceUp("b");
        connectionStore.reportSourceStalled("a");
        expect(connectionStore.status).toBe("connected");
        drain("a", "b");
    });

    it("recovers to connected when the stalled source opens", () => {
        connectionStore.reportSourceStalled("a");
        expect(connectionStore.status).toBe("stalled");
        connectionStore.reportSourceUp("a");
        expect(connectionStore.status).toBe("connected");
        drain("a");
    });

    it("a hard down on a stalled source still drops to disconnected", () => {
        connectionStore.reportSourceStalled("a");
        connectionStore.reportSourceDown("a", "boom");
        expect(connectionStore.status).toBe("disconnected");
        drain("a");
    });
});
