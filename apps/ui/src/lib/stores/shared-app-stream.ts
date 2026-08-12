// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import type { SSEErrorInfo } from "$lib/utils/event-source";
import { createLogger } from "$lib/utils/logger";
import {
    EventManager,
    type AppEventStream,
    type EventHandler,
    type ErrorHandler,
    type OpenHandler,
    type StallHandler,
} from "./event-manager";

// Why this exists: a browser caps concurrent connections to one origin at ~6
// over HTTP/1.1, and that pool is shared across every tab in the whole browser.
// The daemon speaks plain HTTP, and each tab held its own long-lived SSE on
// `/api/stream`, so ~6 open RunWisp tabs saturated the pool — the 7th tab's SSE
// hung in CONNECTING forever, and even plain REST calls queued behind the live
// streams. SharedAppStream fixes the root cause: across all tabs exactly one —
// the elected leader — holds the real EventSource; every other tab (a follower)
// rides a BroadcastChannel and consumes no connection of its own. N tabs → 1
// connection, so the cap is never reached. When the leader tab closes, the Web
// Lock it held is released and a follower is promoted, opening a fresh stream.
//
// The public surface is identical to EventManager's, so stores subscribe the
// same way regardless of whether this tab is the leader or a follower.

const logger = createLogger("SharedAppStream");

// How long a leader tab may stay hidden before it hands the real connection
// back to the cohort. A short grace so flicking between tabs doesn't churn the
// stream, but well under the point a browser freezes a backgrounded tab.
const HIDDEN_RELINQUISH_DELAY_MS = 2000;

/** Cross-tab message envelope sent over the BroadcastChannel. */
type SharedMessage =
    | { t: "event"; type: string; data: string; id?: string }
    | { t: "open" }
    | { t: "error"; info: SSEErrorInfo }
    | { t: "stall" }
    // A tab just joined: the current leader replies with its latest lifecycle
    // so the newcomer reflects the live state without waiting for the next flip.
    | { t: "hello" }
    // A freshly promoted leader asks followers to re-announce their interest so
    // it can subscribe the real stream to every event type any tab needs.
    | { t: "who" }
    | { t: "interest"; types: string[] };

/** Dumb transport: structured-cloneable messages to other tabs, nothing more. */
export interface SharedBus {
    post(message: SharedMessage): void;
    onMessage(handler: (raw: unknown) => void): void;
    close(): void;
}

/** Leader election. `campaign` calls `onElected` if/when this tab wins; the
 * returned function relinquishes leadership (or cancels a pending bid). */
export interface LeaderElector {
    campaign(onElected: () => void): () => void;
}

export interface SharedAppStreamOptions {
    /** SSE path the leader connects to, e.g. `/api/stream`. */
    path: string;
    channelName?: string;
    lockName?: string;
    /**
     * Builds the leader's real connection. Receives the resume-cursor getter so
     * a freshly promoted leader can seed its EventSource from the id the cohort
     * last saw (the server then replays the handoff gap). Defaults to a browser
     * EventManager wired to that seed.
     */
    createLeaderManager?: (seed: () => string | null) => EventManager;
    createBus?: (name: string) => SharedBus;
    createElector?: (name: string) => LeaderElector;
}

export class SharedAppStream implements AppEventStream {
    readonly #path: string;
    readonly #channelName: string;
    readonly #lockName: string;
    readonly #createLeaderManager: (seed: () => string | null) => EventManager;
    readonly #createBus: (name: string) => SharedBus;
    readonly #createElector: (name: string) => LeaderElector;

    // Local fan-out to this tab's subscribers. Plain (non-reactive) collections:
    // this is connection plumbing, never a reactive UI source.
    readonly #handlers = new Map<string, Set<EventHandler>>();
    readonly #openHandlers = new Set<OpenHandler>();
    readonly #errorHandlers = new Set<ErrorHandler>();
    readonly #stallHandlers = new Set<StallHandler>();

    #started = false;
    #bus: SharedBus | null = null;
    #releaseCampaign: (() => void) | null = null;

    // Tab-lifecycle wiring: a leader relinquishes when its tab is hidden/frozen
    // so a visible tab takes over the sole connection. Listeners are torn down
    // via the AbortController on #stop().
    #lifecycleAbort: AbortController | null = null;
    #hiddenTimer: ReturnType<typeof setTimeout> | null = null;

    // Highest SSE event id any tab in the cohort has seen (leader from the real
    // stream, followers from rebroadcasts). Seeds a promoted leader's fresh
    // EventSource so the server replays the handoff gap.
    #lastEventId: string | null = null;

    // Leader-only state.
    #isLeader = false;
    #leader: EventManager | null = null;
    readonly #leaderTypes = new Set<string>();
    // Latest lifecycle the leader observed, replayed to tabs that join late.
    #lastLifecycle: "open" | "error" | "stall" | null = null;
    #lastErrorInfo: SSEErrorInfo | null = null;

    constructor(options: SharedAppStreamOptions) {
        this.#path = options.path;
        this.#channelName = options.channelName ?? "runwisp-app-stream";
        this.#lockName = options.lockName ?? "runwisp-app-stream-leader";
        this.#createLeaderManager =
            options.createLeaderManager ??
            ((seed) => new EventManager({ path: this.#path, initialLastEventId: seed }));
        this.#createBus = options.createBus ?? defaultCreateBus;
        this.#createElector = options.createElector ?? defaultCreateElector;
    }

    /**
     * Whether this tab is actually sharing one connection across tabs (Web Locks
     * + BroadcastChannel present). When false we've degraded to one EventSource
     * per tab, so a stall genuinely can be caused by too many open tabs — UI copy
     * keys off this to decide whether blaming tabs is honest.
     */
    get sharing(): boolean {
        return canShare();
    }

    subscribe(eventType: string, handler: EventHandler): () => void {
        let set = this.#handlers.get(eventType);
        if (!set) {
            set = new Set();
            this.#handlers.set(eventType, set);
        }
        set.add(handler);

        this.#ensureStarted();
        // Tell whichever tab is currently the leader that we want this type, and
        // (if we are the leader) wire it onto the real stream immediately.
        this.#announceInterest();
        this.#leaderEnsureType(eventType);

        return () => {
            this.#unsubscribe(eventType, handler);
        };
    }

    onOpen(handler: OpenHandler): () => void {
        this.#openHandlers.add(handler);
        return () => {
            this.#openHandlers.delete(handler);
        };
    }

    onError(handler: ErrorHandler): () => void {
        this.#errorHandlers.add(handler);
        return () => {
            this.#errorHandlers.delete(handler);
        };
    }

    onStall(handler: StallHandler): () => void {
        this.#stallHandlers.add(handler);
        return () => {
            this.#stallHandlers.delete(handler);
        };
    }

    #unsubscribe(eventType: string, handler: EventHandler): void {
        const set = this.#handlers.get(eventType);
        if (!set) return;
        set.delete(handler);
        if (set.size === 0) this.#handlers.delete(eventType);
        if (this.#totalSubscribers() === 0) this.#stop();
    }

    #totalSubscribers(): number {
        let count = 0;
        for (const set of this.#handlers.values()) count += set.size;
        return count;
    }

    #ensureStarted(): void {
        if (this.#started) return;
        this.#started = true;

        this.#bus = this.#createBus(this.#channelName);
        this.#bus.onMessage((raw) => {
            this.#onBusMessage(raw);
        });
        // Greet the cohort: an existing leader replays its lifecycle to us.
        this.#post({ t: "hello" });

        this.#startCampaign();
        this.#bindLifecycle();
    }

    #startCampaign(): void {
        this.#releaseCampaign = this.#createElector(this.#lockName).campaign(() => {
            this.#becomeLeader();
        });
    }

    // Hand leadership back to the cohort when this tab is hidden or about to be
    // frozen/unloaded. The browser only freezes hidden tabs, so releasing the
    // lock the moment we go hidden (after a short grace) keeps the sole
    // connection on a tab that is still alive — a backgrounded tab can no longer
    // hold it hostage and starve every visible tab. Browser-only; no-op under
    // SSR or where the lifecycle events are unavailable.
    #bindLifecycle(): void {
        if (typeof document === "undefined" || typeof window === "undefined") return;
        const ac = new AbortController();
        this.#lifecycleAbort = ac;
        const opts = { signal: ac.signal };
        document.addEventListener(
            "visibilitychange",
            () => {
                if (document.visibilityState === "hidden") this.#scheduleRelinquish();
                else this.#cancelRelinquish();
            },
            opts,
        );
        // freeze/pagehide are the last chance to relinquish while JS still runs.
        const handoff = (): void => {
            this.#relinquishLeadership();
        };
        document.addEventListener("freeze", handoff, opts);
        window.addEventListener("pagehide", handoff, opts);
    }

    #scheduleRelinquish(): void {
        if (this.#hiddenTimer || !this.#isLeader) return;
        this.#hiddenTimer = setTimeout(() => {
            this.#hiddenTimer = null;
            this.#relinquishLeadership();
        }, HIDDEN_RELINQUISH_DELAY_MS);
    }

    #cancelRelinquish(): void {
        if (this.#hiddenTimer) {
            clearTimeout(this.#hiddenTimer);
            this.#hiddenTimer = null;
        }
    }

    // Drop the real connection, release the leader lock, and re-enter the
    // election from the back of the queue. A still-visible tab is ahead in the
    // queue and is promoted; a lone tab simply re-wins and reopens. Closing the
    // EventManager is quiet (no error broadcast), so followers keep their last
    // lifecycle until the new leader's stream opens — no spurious disconnect.
    #relinquishLeadership(): void {
        this.#cancelRelinquish();
        if (!this.#started || !this.#isLeader) return;
        logger.info("relinquishing leadership (tab hidden/frozen)");
        this.#isLeader = false;
        this.#leaderTypes.clear();
        this.#lastLifecycle = null;
        this.#lastErrorInfo = null;
        if (this.#leader) {
            this.#leader.close();
            this.#leader = null;
        }
        if (this.#releaseCampaign) {
            this.#releaseCampaign();
            this.#releaseCampaign = null;
        }
        this.#startCampaign();
    }

    #becomeLeader(): void {
        if (!this.#started || this.#isLeader) return;
        this.#isLeader = true;
        logger.info("elected leader — opening the shared app-event stream");

        const mgr = this.#createLeaderManager(() => this.#lastEventId);
        this.#leader = mgr;
        mgr.onOpen(() => {
            this.#lastLifecycle = "open";
            this.#lastErrorInfo = null;
            this.#emitOpen();
            this.#post({ t: "open" });
        });
        mgr.onError((info) => {
            this.#lastLifecycle = "error";
            this.#lastErrorInfo = info;
            this.#emitError(info);
            this.#post({ t: "error", info });
        });
        mgr.onStall(() => {
            this.#lastLifecycle = "stall";
            this.#emitStall();
            this.#post({ t: "stall" });
        });

        // Subscribe the real stream to everything this tab already wants, then
        // ask followers to re-announce so we also cover their needs.
        for (const type of this.#handlers.keys()) this.#leaderEnsureType(type);
        this.#post({ t: "who" });
    }

    /** When leader, ensure the real stream is forwarding `eventType` to all tabs. */
    #leaderEnsureType(eventType: string): void {
        const mgr = this.#leader;
        if (!this.#isLeader || !mgr || this.#leaderTypes.has(eventType)) return;
        this.#leaderTypes.add(eventType);
        mgr.subscribe(eventType, (data, id) => {
            if (id) this.#lastEventId = id;
            this.#dispatch(eventType, data);
            this.#post({ t: "event", type: eventType, data, ...(id && { id }) });
        });
    }

    #announceInterest(): void {
        this.#post({ t: "interest", types: [...this.#handlers.keys()] });
    }

    #onBusMessage(raw: unknown): void {
        const msg = parseSharedMessage(raw);
        if (!msg) return;
        // A leader never receives its own broadcasts (BroadcastChannel doesn't
        // echo), so leader vs follower see disjoint message sets — split them.
        if (this.#isLeader) this.#handleAsLeader(msg);
        else this.#handleAsFollower(msg);
    }

    #handleAsLeader(msg: SharedMessage): void {
        if (msg.t === "hello") {
            // A newcomer arrived: replay our latest lifecycle so it syncs.
            this.#replayLifecycle();
        } else if (msg.t === "interest") {
            for (const type of msg.types) this.#leaderEnsureType(type);
        }
    }

    #handleAsFollower(msg: SharedMessage): void {
        switch (msg.t) {
            case "event":
                if (msg.id) this.#lastEventId = msg.id;
                this.#dispatch(msg.type, msg.data);
                return;
            case "open":
                this.#emitOpen();
                return;
            case "error":
                this.#emitError(msg.info);
                return;
            case "stall":
                this.#emitStall();
                return;
            case "who":
                // A new leader is gathering interests: re-announce ours.
                this.#announceInterest();
                return;
            default:
                // "hello" / "interest" are leader-directed; followers ignore.
                return;
        }
    }

    #replayLifecycle(): void {
        switch (this.#lastLifecycle) {
            case "open":
                this.#post({ t: "open" });
                return;
            case "error":
                if (this.#lastErrorInfo) this.#post({ t: "error", info: this.#lastErrorInfo });
                return;
            case "stall":
                this.#post({ t: "stall" });
                return;
            case null:
                return;
        }
    }

    #dispatch(eventType: string, data: string): void {
        const set = this.#handlers.get(eventType);
        if (!set) return;
        for (const handler of set) {
            try {
                handler(data);
            } catch (err) {
                logger.error(`handler for ${eventType} threw`, err);
            }
        }
    }

    #emitOpen(): void {
        for (const handler of this.#openHandlers) {
            try {
                handler();
            } catch (err) {
                logger.warn("onOpen handler threw", err);
            }
        }
    }

    #emitError(info: SSEErrorInfo): void {
        for (const handler of this.#errorHandlers) {
            try {
                handler(info);
            } catch (err) {
                logger.warn("onError handler threw", err);
            }
        }
    }

    #emitStall(): void {
        for (const handler of this.#stallHandlers) {
            try {
                handler();
            } catch (err) {
                logger.warn("onStall handler threw", err);
            }
        }
    }

    #post(message: SharedMessage): void {
        if (!this.#bus) return;
        try {
            this.#bus.post(message);
        } catch (err) {
            logger.warn("failed to post to shared bus", err);
        }
    }

    #stop(): void {
        if (!this.#started) return;
        this.#started = false;

        this.#cancelRelinquish();
        if (this.#lifecycleAbort) {
            this.#lifecycleAbort.abort();
            this.#lifecycleAbort = null;
        }
        if (this.#releaseCampaign) {
            this.#releaseCampaign();
            this.#releaseCampaign = null;
        }
        if (this.#leader) {
            this.#leader.close();
            this.#leader = null;
        }
        if (this.#bus) {
            this.#bus.close();
            this.#bus = null;
        }
        this.#isLeader = false;
        this.#leaderTypes.clear();
        this.#lastLifecycle = null;
        this.#lastErrorInfo = null;
        // Full teardown: the next start is a fresh client that re-seeds via REST,
        // so forget the resume cursor. (A leader relinquish deliberately keeps it,
        // so the promoted tab resumes across the handoff.)
        this.#lastEventId = null;
    }
}

// ─── message parsing (the bus delivers unknown structured-clone payloads) ─────

function isRecord(value: unknown): value is Record<string, unknown> {
    return typeof value === "object" && Boolean(value);
}

function parseSharedMessage(raw: unknown): SharedMessage | null {
    if (!isRecord(raw)) return null;
    switch (raw.t) {
        case "open":
            return { t: "open" };
        case "stall":
            return { t: "stall" };
        case "hello":
            return { t: "hello" };
        case "who":
            return { t: "who" };
        case "error":
            return { t: "error", info: parseErrorInfo(raw.info) };
        case "event":
            return parseEventMessage(raw.type, raw.data, raw.id);
        case "interest":
            return { t: "interest", types: parseStringArray(raw.types) };
        default:
            return null;
    }
}

function parseEventMessage(type: unknown, data: unknown, id: unknown): SharedMessage | null {
    if (typeof type !== "string" || typeof data !== "string") return null;
    return { t: "event", type, data, ...(typeof id === "string" && { id }) };
}

function parseStringArray(value: unknown): string[] {
    return Array.isArray(value) ? value.filter((x): x is string => typeof x === "string") : [];
}

function parseErrorInfo(value: unknown): SSEErrorInfo {
    const info: SSEErrorInfo = {};
    if (!isRecord(value)) return info;
    if (typeof value.status === "number") info.status = value.status;
    if (typeof value.message === "string") info.message = value.message;
    if (typeof value.readyState === "number") info.readyState = value.readyState;
    if (typeof value.url === "string") info.url = value.url;
    return info;
}

// ─── default browser transports ──────────────────────────────────────────────

// Cross-tab sharing needs BOTH primitives: BroadcastChannel to ferry events and
// Web Locks to elect a single leader. If either is missing we degrade to the
// old model — every tab is its own leader with its own EventSource — rather than
// risk a follower that can never receive anything.
function canShare(): boolean {
    return (
        typeof BroadcastChannel !== "undefined" &&
        typeof navigator !== "undefined" &&
        "locks" in navigator
    );
}

function defaultCreateBus(name: string): SharedBus {
    if (typeof BroadcastChannel === "undefined") {
        return { post: () => {}, onMessage: () => {}, close: () => {} };
    }
    const channel = new BroadcastChannel(name);
    return {
        post: (message) => {
            channel.postMessage(message);
        },
        onMessage: (handler) => {
            channel.onmessage = (event: MessageEvent) => {
                handler(event.data);
            };
        },
        close: () => {
            channel.close();
        },
    };
}

function defaultCreateElector(name: string): LeaderElector {
    if (!canShare()) {
        // No coordination available: win immediately and run standalone.
        return {
            campaign: (onElected) => {
                onElected();
                return () => {};
            },
        };
    }
    return {
        campaign: (onElected) => {
            const abort = new AbortController();
            let release: (() => void) | null = null;
            navigator.locks
                .request(name, { signal: abort.signal }, () => {
                    // Holding the lock === being the leader. Keep the callback's
                    // promise pending so the lock is held until we release it;
                    // when this tab closes the browser auto-releases it and a
                    // waiting tab is promoted.
                    return new Promise<void>((resolve) => {
                        release = resolve;
                        onElected();
                    });
                })
                .catch(() => {
                    // AbortError when we cancel a pending bid before winning.
                });
            return () => {
                if (release) release();
                else abort.abort();
            };
        },
    };
}
