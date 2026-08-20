// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

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

    error(details: { message?: string; status?: number } = { message: "boom" }): void {
        this.onerror?.(Object.assign(new Event("error"), details));
    }

    fire(eventType: string, payload: unknown): void {
        this.#target.dispatchEvent(new MessageEvent(eventType, { data: JSON.stringify(payload) }));
    }

    fireWithId(eventType: string, payload: unknown, lastEventId: string): void {
        this.#target.dispatchEvent(
            new MessageEvent(eventType, { data: JSON.stringify(payload), lastEventId }),
        );
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

    // Inject an arbitrary (possibly malformed) payload to every connected tab, as
    // if a misbehaving peer broadcast it. Used to exercise message parsing.
    postRaw(raw: unknown): void {
        for (const entry of this.#entries) entry.handler(raw);
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

// Stubs document + window so the tab-lifecycle listeners (visibilitychange /
// freeze / pagehide) can be driven under the `node` test environment. All
// SharedAppStream instances in a test share this one registry — exactly like
// real tabs sharing a document — so `fire` reaches every tab's handler.
function stubLifecycle() {
    const listeners = new Map<string, Set<() => void>>();
    let visibilityState = "visible";
    const add = (type: string, handler: () => void): void => {
        let s = listeners.get(type);
        if (!s) {
            s = new Set();
            listeners.set(type, s);
        }
        s.add(handler);
    };
    const remove = (type: string, handler: () => void): void => {
        listeners.get(type)?.delete(handler);
    };
    const doc = {
        addEventListener: add,
        removeEventListener: remove,
        get visibilityState() {
            return visibilityState;
        },
    };
    vi.stubGlobal("document", doc);
    vi.stubGlobal("window", { addEventListener: add, removeEventListener: remove });
    return {
        setVisibility(v: "visible" | "hidden") {
            visibilityState = v;
        },
        fire(type: string) {
            for (const h of listeners.get(type) ?? []) h();
        },
    };
}

// ─── harness ─────────────────────────────────────────────────────────────────

function makeWorld() {
    const bus = new BusHub();
    const election = new ElectionHub();
    const tabs: Tab[] = [];

    function makeTab() {
        let leaderES: FakeEventSource | null = null;
        const stream = new SharedAppStream({
            path: "/api/events/stream",
            createBus: () => bus.create(),
            createElector: () => election.create(),
            createLeaderManager: () => {
                const es = new FakeEventSource();
                leaderES = es;
                return new EventManager({
                    path: "/api/events/stream",
                    createEventSource: () => es,
                    getApiUrl: () => "http://test",
                });
            },
        });
        const tab: Tab = { stream, leaderES: () => leaderES };
        tabs.push(tab);
        return tab;
    }

    return { makeTab, bus };
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

    it("a promoted leader resumes from the id the cohort last saw", () => {
        const bus = new BusHub();
        const election = new ElectionHub();
        const connectUrls: string[] = [];

        function makeSeedTab() {
            let leaderES: FakeEventSource | null = null;
            const stream = new SharedAppStream({
                path: "/api/events/stream",
                createBus: () => bus.create(),
                createElector: () => election.create(),
                createLeaderManager: (seed) => {
                    const es = new FakeEventSource();
                    leaderES = es;
                    return new EventManager({
                        path: "/api/events/stream",
                        initialLastEventId: seed,
                        createEventSource: (url) => {
                            connectUrls.push(url);
                            return es;
                        },
                        getApiUrl: () => "http://test",
                    });
                },
            });
            return { stream, leaderES: () => leaderES };
        }

        const a = makeSeedTab();
        const b = makeSeedTab();

        // A wins the election (subscribes first); B rides the bus as a follower.
        const unsubA = a.stream.subscribe("run.created", () => {});
        b.stream.subscribe("run.created", () => {});

        // Leader A receives an event carrying id "5"; it dispatches locally and
        // rebroadcasts the id, so BOTH tabs record it as the high-water cursor.
        a.leaderES()?.open();
        a.leaderES()?.fireWithId("run.created", { run: { id: "r1" } }, "5");

        // A drops its last subscriber and goes away → B is promoted and opens a
        // fresh EventSource, which must resume from id 5 (native Last-Event-ID is
        // empty on a brand-new EventSource) so the server replays the handoff gap.
        unsubA();

        expect(b.leaderES()).not.toBeNull();
        expect(connectUrls.at(-1)).toBe("http://test/api/events/stream?lastEventId=5");
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

    it("propagates an error lifecycle from leader to followers with details", () => {
        const { makeTab } = makeWorld();
        const leaderTab = makeTab();
        const followerTab = makeTab();

        const leaderError = vi.fn();
        const followerError = vi.fn();
        leaderTab.stream.onError(leaderError);
        followerTab.stream.onError(followerError);

        leaderTab.stream.subscribe("system", () => {});
        followerTab.stream.subscribe("system", () => {});

        leaderTab.leaderES()?.error({ message: "dropped", status: 503 });

        expect(leaderError).toHaveBeenCalledTimes(1);
        expect(followerError).toHaveBeenCalledTimes(1);
        // The follower receives the full info, reconstructed from the bus payload.
        expect(followerError.mock.calls.at(0)?.[0]).toMatchObject({
            message: "dropped",
            status: 503,
        });
    });

    it("syncs a late-joining follower to the current error state", () => {
        const { makeTab } = makeWorld();
        const leaderTab = makeTab();

        leaderTab.stream.subscribe("system", () => {});
        leaderTab.leaderES()?.error({ message: "boom", status: 500 });

        const followerTab = makeTab();
        const followerError = vi.fn();
        followerTab.stream.onError(followerError);
        followerTab.stream.subscribe("system", () => {});

        expect(followerError).toHaveBeenCalledTimes(1);
        expect(followerError.mock.calls.at(0)?.[0]).toMatchObject({ message: "boom", status: 500 });
    });

    it("syncs a late-joining follower to the current stall state", () => {
        vi.useFakeTimers();
        try {
            const { makeTab } = makeWorld();
            const leaderTab = makeTab();

            leaderTab.stream.subscribe("system", () => {});
            vi.advanceTimersByTime(SSE_CONFIG.OPEN_TIMEOUT); // leader stalls

            const followerTab = makeTab();
            const followerStall = vi.fn();
            followerTab.stream.onStall(followerStall);
            followerTab.stream.subscribe("system", () => {});

            expect(followerStall).toHaveBeenCalledTimes(1);
        } finally {
            vi.useRealTimers();
        }
    });

    it("a throwing event handler does not break the others", () => {
        const { makeTab } = makeWorld();
        const tab = makeTab();
        const after = vi.fn();
        tab.stream.subscribe("run.created", () => {
            throw new Error("handler boom");
        });
        tab.stream.subscribe("run.created", after);

        tab.leaderES()?.open();
        tab.leaderES()?.fire("run.created", { id: "r1" });

        expect(after).toHaveBeenCalledTimes(1);
    });

    it("a throwing lifecycle handler is caught", () => {
        const { makeTab } = makeWorld();
        const tab = makeTab();
        const secondOpen = vi.fn();
        tab.stream.onOpen(() => {
            throw new Error("open boom");
        });
        tab.stream.onOpen(secondOpen);

        tab.stream.subscribe("system", () => {});
        tab.leaderES()?.open();

        expect(secondOpen).toHaveBeenCalledTimes(1);
    });

    it("a lifecycle handler can unsubscribe", () => {
        const { makeTab } = makeWorld();
        const tab = makeTab();
        const onOpen = vi.fn();
        const off = tab.stream.onOpen(onOpen);
        tab.stream.onError(() => {})();
        tab.stream.onStall(() => {})();

        off();
        tab.stream.subscribe("system", () => {});
        tab.leaderES()?.open();

        expect(onOpen).not.toHaveBeenCalled();
    });

    it("stops the connection when the last subscriber leaves and restarts on resubscribe", () => {
        const { makeTab } = makeWorld();
        const tab = makeTab();

        const off1 = tab.stream.subscribe("a", () => {});
        const off2 = tab.stream.subscribe("b", () => {});
        const first = tab.leaderES();
        expect(first).not.toBeNull();

        off1();
        expect(first?.readyState).not.toBe(2); // still other subscribers
        off2();
        expect(first?.readyState).toBe(2); // closed once empty

        // Resubscribing spins up a fresh leader connection.
        tab.stream.subscribe("a", () => {});
        const second = tab.leaderES();
        expect(second).not.toBeNull();
        expect(second).not.toBe(first);
    });

    it("ignores malformed bus messages", () => {
        const { makeTab, bus } = makeWorld();
        const leaderTab = makeTab();
        const followerTab = makeTab();
        leaderTab.stream.subscribe("system", () => {});
        const received: string[] = [];
        followerTab.stream.subscribe("run.created", (d) => received.push(d));

        // None of these should throw or deliver anything.
        bus.postRaw("not an object");
        bus.postRaw(null);
        bus.postRaw({ t: "unknown-kind" });
        bus.postRaw({ t: "event", type: 123, data: "x" }); // non-string type
        bus.postRaw({ t: "hello" }); // leader-directed; follower ignores

        expect(received).toHaveLength(0);
    });

    it("reports sharing as unavailable when Web Locks are missing", () => {
        vi.stubGlobal("navigator", {}); // no `locks`
        try {
            const stream = new SharedAppStream({ path: "/api/events/stream" });
            expect(stream.sharing).toBe(false);
        } finally {
            vi.unstubAllGlobals();
        }
    });

    it("degrades to standalone (default bus, immediate election) without Web Locks", () => {
        vi.stubGlobal("navigator", {}); // no `locks` → canShare() is false
        try {
            let leaderES: FakeEventSource | null = null;
            const stream = new SharedAppStream({
                path: "/api/events/stream",
                // Bus uses the real default (BroadcastChannel); the elector uses the
                // no-lock fallback that wins immediately. Only the leader's real
                // connection is stubbed.
                createLeaderManager: () => {
                    const es = new FakeEventSource();
                    leaderES = es;
                    return new EventManager({
                        path: "/api/events/stream",
                        createEventSource: () => es,
                        getApiUrl: () => "http://test",
                    });
                },
            });

            // Read through a getter so the type stays the declared union (a direct
            // read narrows to its initial null, since the assignment is in a callback).
            const getLeader = () => leaderES;
            const received: string[] = [];
            const off = stream.subscribe("run.created", (d) => received.push(d));
            // The no-lock fallback elects this tab immediately, so it owns the stream.
            const es = getLeader();
            if (!es) throw new Error("expected the fallback to elect a leader");
            es.open();
            es.fire("run.created", { id: "solo" });
            expect(received[0]).toContain("solo");

            off(); // closes the real BroadcastChannel so the test doesn't leak it
        } finally {
            vi.unstubAllGlobals();
        }
    });

    it("a hidden leader hands the connection to a visible tab after the grace delay", () => {
        vi.useFakeTimers();
        const lc = stubLifecycle();
        try {
            const { makeTab } = makeWorld();
            const leaderTab = makeTab();
            const followerTab = makeTab();

            leaderTab.stream.subscribe("run.created", () => {});
            const followerReceived: string[] = [];
            followerTab.stream.subscribe("run.created", (d) => followerReceived.push(d));

            const leaderES = leaderTab.leaderES();
            expect(leaderES).not.toBeNull();
            expect(followerTab.leaderES()).toBeNull();

            // Leader tab goes hidden, but the grace period hasn't elapsed yet.
            lc.setVisibility("hidden");
            lc.fire("visibilitychange");
            vi.advanceTimersByTime(1000);
            expect(followerTab.leaderES()).toBeNull(); // still leading

            // Grace elapses → leader drops its stream and the follower is promoted.
            vi.advanceTimersByTime(2000);
            expect(leaderES?.readyState).toBe(2); // old leader connection closed
            expect(followerTab.leaderES()).not.toBeNull();

            followerTab.leaderES()?.open();
            followerTab.leaderES()?.fire("run.created", { run: { id: "r9" } });
            expect(followerReceived).toHaveLength(1);
            expect(followerReceived[0]).toContain("r9");
        } finally {
            vi.useRealTimers();
            vi.unstubAllGlobals();
        }
    });

    it("a quick hide→show keeps leadership (no churn on a glance away)", () => {
        vi.useFakeTimers();
        const lc = stubLifecycle();
        try {
            const { makeTab } = makeWorld();
            const leaderTab = makeTab();
            const followerTab = makeTab();

            leaderTab.stream.subscribe("system", () => {});
            followerTab.stream.subscribe("system", () => {});

            const originalLeaderES = leaderTab.leaderES();
            expect(originalLeaderES).not.toBeNull();
            expect(followerTab.leaderES()).toBeNull();

            lc.setVisibility("hidden");
            lc.fire("visibilitychange");
            vi.advanceTimersByTime(1000); // within the grace window
            lc.setVisibility("visible");
            lc.fire("visibilitychange"); // cancels the pending relinquish
            vi.advanceTimersByTime(5000);

            // Leadership never moved: same connection, follower still idle.
            expect(followerTab.leaderES()).toBeNull();
            expect(leaderTab.leaderES()).toBe(originalLeaderES);
        } finally {
            vi.useRealTimers();
            vi.unstubAllGlobals();
        }
    });

    it("pagehide relinquishes leadership immediately (last chance before unload/freeze)", () => {
        const lc = stubLifecycle();
        try {
            const { makeTab } = makeWorld();
            const leaderTab = makeTab();
            const followerTab = makeTab();

            leaderTab.stream.subscribe("run.created", () => {});
            followerTab.stream.subscribe("run.created", () => {});
            expect(followerTab.leaderES()).toBeNull();

            lc.fire("pagehide"); // no grace timer — immediate handoff
            expect(followerTab.leaderES()).not.toBeNull();
        } finally {
            vi.unstubAllGlobals();
        }
    });

    it("uses Web Locks for election when available", () => {
        const requested: string[] = [];
        const fakeLocks = {
            request: (name: string, opts: unknown, cb: () => Promise<void>) => {
                requested.push(name);
                void opts;
                return cb();
            },
        };
        // Present both primitives so canShare() takes the real-lock branch.
        vi.stubGlobal("navigator", { locks: fakeLocks });
        try {
            let leaderES: FakeEventSource | null = null;
            const stream = new SharedAppStream({
                path: "/api/events/stream",
                createBus: () => ({ post: () => {}, onMessage: () => {}, close: () => {} }),
                createLeaderManager: () => {
                    const es = new FakeEventSource();
                    leaderES = es;
                    return new EventManager({
                        path: "/api/events/stream",
                        createEventSource: () => es,
                        getApiUrl: () => "http://test",
                    });
                },
            });

            expect(stream.sharing).toBe(true);
            const off = stream.subscribe("system", () => {});
            // Holding the lock === being elected leader; the real stream opened.
            expect(requested).toContain("runwisp-app-stream-leader");
            expect(leaderES).not.toBeNull();

            off(); // releases the held lock promise
        } finally {
            vi.unstubAllGlobals();
        }
    });
});

// Keep real timers the default between the timer-using test and others.
beforeEach(() => {
    vi.useRealTimers();
});
afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
});
