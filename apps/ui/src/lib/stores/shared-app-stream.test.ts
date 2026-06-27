// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { EventManager } from "./event-manager";
import { SharedAppStream, type LeaderElector, type SharedBus } from "./shared-app-stream";
import { SSE_CONFIG } from "$lib/config/constants";
import type { SSEStream } from "$lib/adapters/browser";

// ─── fakes ───────────────────────────────────────────────────────────────────

// FakeEventSource the leader's EventManager drives. `fire` mimics a named SSE
// message; `open` mimics the connection opening. Nothing fires on its own.
class FakeEventSource implements SSEStream {
    readyState = 0;
    onopen: ((ev: Event) => unknown) | null = null;
    onerror: ((ev: Event) => unknown) | null = null;
    onmessage: ((ev: MessageEvent) => unknown) | null = null;
    readonly #target = new EventTarget();

    close(): void {
        this.readyState = 2;
    }

    addEventListener(type: string, listener: (event: MessageEvent) => void): void {
        this.#target.addEventListener(type, (event: Event) => {
            if (event instanceof MessageEvent) listener(event);
        });
    }

    open(): void {
        this.readyState = 1;
        this.onopen?.(new Event("open"));
    }

    fire(eventType: string, payload: unknown): void {
        this.#target.dispatchEvent(new MessageEvent(eventType, { data: JSON.stringify(payload) }));
    }
}

// In-memory BroadcastChannel: post reaches every OTHER connected bus, never the
// sender — matching real BroadcastChannel semantics the code relies on.
class BusHub {
    readonly #entries = new Set<{ handler: (raw: unknown) => void }>();

    create(): SharedBus {
        const entry = { handler: (_: unknown) => {} };
        return {
            post: (message) => {
                for (const other of this.#entries) {
                    if (other !== entry) other.handler(message);
                }
            },
            onMessage: (handler) => {
                entry.handler = handler;
                this.#entries.add(entry);
            },
            close: () => {
                this.#entries.delete(entry);
            },
        };
    }
}

// Single-leader election: the head of the queue is the leader. Releasing pops it
// and promotes the next — exactly how a Web Lock hands off when a tab closes.
class ElectionHub {
    readonly #queue: { onElected: () => void; elected: boolean }[] = [];

    create(): LeaderElector {
        return {
            campaign: (onElected) => {
                const entry = { onElected, elected: false };
                this.#queue.push(entry);
                this.#reconcile();
                return () => {
                    const i = this.#queue.indexOf(entry);
                    if (i >= 0) this.#queue.splice(i, 1);
                    this.#reconcile();
                };
            },
        };
    }

    #reconcile(): void {
        if (this.#queue.some((e) => e.elected)) return;
        const head = this.#queue[0];
        if (!head) return;
        head.elected = true;
        head.onElected();
    }
}

// ─── harness ─────────────────────────────────────────────────────────────────

function makeWorld() {
    const bus = new BusHub();
    const election = new ElectionHub();
    const tabs: Tab[] = [];

    function makeTab() {
        let leaderES: FakeEventSource | null = null;
        const stream = new SharedAppStream({
            path: "/api/stream",
            createBus: () => bus.create(),
            createElector: () => election.create(),
            createLeaderManager: () => {
                const es = new FakeEventSource();
                leaderES = es;
                return new EventManager({
                    path: "/api/stream",
                    createEventSource: () => es,
                    getApiUrl: () => "http://test",
                });
            },
        });
        const tab: Tab = { stream, leaderES: () => leaderES };
        tabs.push(tab);
        return tab;
    }

    return { makeTab };
}

interface Tab {
    stream: SharedAppStream;
    leaderES: () => FakeEventSource | null;
}

describe("SharedAppStream", () => {
    it("the leader holds the real connection and delivers events to itself", () => {
        const { makeTab } = makeWorld();
        const a = makeTab();
        const received: string[] = [];

        a.stream.subscribe("run.created", (data) => received.push(data));
        const es = a.leaderES();
        expect(es).not.toBeNull();
        es?.open();
        es?.fire("run.created", { run: { id: "r1" } });

        expect(received).toHaveLength(1);
        expect(received[0]).toContain("r1");
    });

    it("a follower rides the bus and consumes no connection of its own", () => {
        const { makeTab } = makeWorld();
        const leaderTab = makeTab();
        const followerTab = makeTab();

        // Leader subscribes first → wins election. Follower never opens an ES.
        leaderTab.stream.subscribe("system", () => {});
        const followerReceived: string[] = [];
        followerTab.stream.subscribe("run.created", (data) => followerReceived.push(data));

        expect(leaderTab.leaderES()).not.toBeNull();
        expect(followerTab.leaderES()).toBeNull();

        // The leader picked up the follower's interest, so a run.created the
        // leader never subscribed to locally still reaches the follower.
        leaderTab.leaderES()?.open();
        leaderTab.leaderES()?.fire("run.created", { run: { id: "r2" } });

        expect(followerReceived).toHaveLength(1);
        expect(followerReceived[0]).toContain("r2");
    });

    it("propagates open lifecycle from leader to followers", () => {
        const { makeTab } = makeWorld();
        const leaderTab = makeTab();
        const followerTab = makeTab();

        const leaderOpen = vi.fn();
        const followerOpen = vi.fn();
        leaderTab.stream.onOpen(leaderOpen);
        followerTab.stream.onOpen(followerOpen);

        leaderTab.stream.subscribe("system", () => {});
        followerTab.stream.subscribe("system", () => {});

        leaderTab.leaderES()?.open();

        expect(leaderOpen).toHaveBeenCalledTimes(1);
        expect(followerOpen).toHaveBeenCalledTimes(1);
    });

    it("propagates a stall from the leader to followers", () => {
        vi.useFakeTimers();
        try {
            const { makeTab } = makeWorld();
            const leaderTab = makeTab();
            const followerTab = makeTab();

            const followerStall = vi.fn();
            followerTab.stream.onStall(followerStall);

            leaderTab.stream.subscribe("system", () => {});
            followerTab.stream.subscribe("system", () => {});

            // Leader's connection hangs open → its EventManager stalls → broadcast.
            vi.advanceTimersByTime(SSE_CONFIG.OPEN_TIMEOUT);

            expect(followerStall).toHaveBeenCalledTimes(1);
        } finally {
            vi.useRealTimers();
        }
    });

    it("promotes a follower when the leader tab goes away", () => {
        const { makeTab } = makeWorld();
        const leaderTab = makeTab();
        const followerTab = makeTab();

        const unsubLeader = leaderTab.stream.subscribe("run.created", () => {});
        const followerReceived: string[] = [];
        followerTab.stream.subscribe("run.created", (data) => followerReceived.push(data));

        expect(followerTab.leaderES()).toBeNull();

        // Leader tab drops its last subscriber → relinquishes leadership.
        unsubLeader();

        // The follower is promoted and opens its own real connection.
        expect(followerTab.leaderES()).not.toBeNull();
        followerTab.leaderES()?.open();
        followerTab.leaderES()?.fire("run.created", { run: { id: "r3" } });

        expect(followerReceived).toHaveLength(1);
        expect(followerReceived[0]).toContain("r3");
    });

    it("syncs a late-joining follower to the current open state", () => {
        const { makeTab } = makeWorld();
        const leaderTab = makeTab();

        leaderTab.stream.subscribe("system", () => {});
        leaderTab.leaderES()?.open(); // leader is connected before the 2nd tab exists

        const followerTab = makeTab();
        const followerOpen = vi.fn();
        followerTab.stream.onOpen(followerOpen);
        // Joining (subscribing) sends "hello"; the leader replays its lifecycle.
        followerTab.stream.subscribe("system", () => {});

        expect(followerOpen).toHaveBeenCalledTimes(1);
    });
});

// Keep real timers the default between the timer-using test and others.
beforeEach(() => {
    vi.useRealTimers();
});
afterEach(() => {
    vi.useRealTimers();
});
