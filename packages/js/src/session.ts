// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// The app's connection to the daemon: `GET /api/local/app` on its socket,
// upgraded to newline-delimited JSON. The messages mirror
// apps/runwisp/internal/apphost/host.go.

import { createRequire } from "node:module";
import { connect, type Socket } from "node:net";
import { createInterface } from "node:readline";

import { field } from "./daemon.ts";

/** The Upgrade token: the app protocol and its version, as in apps/runwisp/internal/apphost. */
export const PROTOCOL = "runwisp-app/1";

/** Who connects: copies of one app share a daemon, a different app is refused. */
export interface AppIdentity {
    app: string;
    pid: number;
}

/** A run of one of this app's tasks or services. */
export interface RunRequest {
    run: string;
    /** The task or service name. */
    task: string;
    /** The unit's env, secrets and env params, resolved by RunWisp. */
    env: Record<string, string>;
    params: Record<string, string>;
    /** The firing, for a run the schedule started; absent for one started by hand. */
    at?: Date;
}

export type ConfigReply = { ok: true } | { ok: false; error: string; restart: boolean };

/** Each gets the session, which may arrive before open() resolves. */
export interface SessionEvents {
    /** Whether this copy of the app runs the tasks and supplies the config. Others stand by. */
    role(session: Session, active: boolean): void;
    /** This active copy is now the only app connected. */
    alone(session: Session): void;
    run(session: Session, request: RunRequest): void;
    stop(run: string): void;
    /** RunWisp skipped a firing of task (on_overlap: "skip") scheduled for at. */
    overlap(task: string, at: Date): void;
    /** Firings of task were missed while RunWisp was down, the latest at at. */
    missed(task: string, at: Date): void;
    closed(session: Session): void;
}

export type Stream = "stdout" | "stderr";

function strings(value: unknown): Record<string, string> {
    if (typeof value !== "object" || value === null) return {};
    return Object.fromEntries(
        Object.entries(value).filter(
            (entry): entry is [string, string] => typeof entry[1] === "string",
        ),
    );
}

/** This package's version, for errors about a runwisp binary that doesn't match it. */
function packageVersion(): string {
    try {
        const pkg: unknown = createRequire(import.meta.url)("../package.json");
        const version = field(pkg, "version");
        return typeof version === "string" ? version : "?";
    } catch {
        return "?";
    }
}

/** Why the daemon refused the app connection, from its HTTP answer. */
export function refusal(status: string, head: string, body: string): Error {
    if (status === "426") {
        const theirs = /^upgrade:\s*runwisp-app\/(\d+)\s*$/im.exec(head)?.[1];
        const age = Number(theirs) > Number(PROTOCOL.split("/")[1]) ? "newer" : "older";
        return new Error(
            `this runwisp binary is ${age} than @runwisp/js ${packageVersion()} supports (app protocol ${theirs ?? "unknown"}, this package speaks ${PROTOCOL}): install matching versions of runwisp and @runwisp/js`,
        );
    }
    const why = body.trim();
    return new Error(`RunWisp refused the app connection (HTTP ${status})${why ? `: ${why}` : ""}`);
}

export class Session {
    active = false;
    /** Whether this copy is the only app connected, as the daemon last said. */
    alone = false;
    /** The active copy's pid, as told to a standby copy; 0 when unknown. */
    activePid = 0;
    private readonly socket: Socket;
    private readonly events: SessionEvents;
    private readonly replies: ((reply: ConfigReply) => void)[] = [];
    private readonly gone: Promise<void>;

    private constructor(socket: Socket, events: SessionEvents) {
        this.socket = socket;
        this.events = events;
        // A daemon going away must never crash the app: closed() handles it.
        socket.on("error", () => undefined);
        this.gone = new Promise((resolve) => {
            socket.once("close", () => {
                for (const reply of this.replies.splice(0)) {
                    reply({
                        ok: false,
                        error: "RunWisp stopped before it applied the config",
                        restart: false,
                    });
                }
                resolve();
                events.closed(this);
            });
        });
        const lines = createInterface({ input: socket });
        lines.on("error", () => undefined);
        lines.on("line", (line) => {
            this.receive(line);
        });
    }

    /**
     * Connects, and resolves once the daemon said whether this copy is active.
     * The upgrade is written by hand: Bun before 1.4 never emits node:http's
     * "upgrade" event.
     */
    static open(socketPath: string, events: SessionEvents, who: AppIdentity): Promise<Session> {
        return new Promise((resolve, reject) => {
            const socket = connect(socketPath);
            let head = Buffer.alloc(0);
            // Set once the daemon answered with an error: why it refused.
            let refused: (() => Error) | undefined;
            const onData = (chunk: Buffer) => {
                head = Buffer.concat([head, chunk]);
                const end = head.indexOf("\r\n\r\n");
                if (end === -1) return;
                socket.off("data", onData);
                const headText = head.toString("latin1", 0, end);
                const status = /^HTTP\/1\.[01] (\d{3})/.exec(headText)?.[1] ?? "?";
                if (status !== "101") {
                    // The reason is the body: read it, then hang up (the daemon keeps the connection alive).
                    const length = Number(/^content-length:\s*(\d+)/im.exec(headText)?.[1] ?? 0);
                    let body = head.subarray(end + 4);
                    refused = () => refusal(status, headText, body.toString("utf8"));
                    const onBody = (more?: Buffer) => {
                        if (more) body = Buffer.concat([body, more]);
                        if (body.length >= length) socket.destroy();
                    };
                    socket.on("data", onBody);
                    onBody();
                    return;
                }
                socket.pause();
                const rest = head.subarray(end + 4);
                if (rest.length > 0) socket.unshift(rest);
                new Session(socket, {
                    ...events,
                    role: (session, active) => {
                        resolve(session);
                        events.role(session, active);
                    },
                    closed: (session) => {
                        reject(new Error("RunWisp closed the app connection"));
                        events.closed(session);
                    },
                });
            };
            socket.on("data", onData);
            socket.once("error", reject);
            socket.once("close", () => {
                reject(refused ? refused() : new Error("RunWisp closed the app connection"));
            });
            const query = new URLSearchParams({ app: who.app, pid: String(who.pid) });
            socket.write(
                `GET /api/local/app?${query.toString()} HTTP/1.1\r\nHost: runwisp\r\nConnection: Upgrade\r\nUpgrade: ${PROTOCOL}\r\n\r\n`,
            );
        });
    }

    private send(message: Record<string, unknown>): void {
        if (this.socket.writable) this.socket.write(JSON.stringify(message) + "\n");
    }

    /** With partial, line is a piece of a line that the next output continues. */
    output(run: string, stream: Stream, line: string, partial = false): void {
        this.send(
            partial
                ? { type: "output", run, stream, line, partial }
                : { type: "output", run, stream, line },
        );
    }

    exit(run: string, code: number): void {
        this.send({ type: "exit", run, code });
    }

    /** Whether the connection keeps the process alive. */
    hold(held: boolean): void {
        if (held) this.socket.ref();
        else this.socket.unref();
    }

    /** Replaces the daemon's config with doc (JSON), as a reload would. One at a time. */
    config(doc: string): Promise<ConfigReply> {
        return new Promise((resolve) => {
            if (!this.socket.writable) {
                resolve({ ok: false, error: "RunWisp is not connected", restart: false });
                return;
            }
            // Replies carry no id: they are matched to requests in order.
            if (this.replies.length > 0) {
                resolve({ ok: false, error: "a config push is already pending", restart: false });
                return;
            }
            this.replies.push(resolve);
            this.socket.write(`{"type":"config","doc":${doc}}\n`);
        });
    }

    /**
     * Says this copy is shutting down, not restarting. The next copy takes
     * over and this one hears it is on standby; when it was the last, RunWisp
     * stops once this copy disconnects, so its runs can finish first.
     */
    leave(): void {
        this.send({ type: "close" });
    }

    close(): Promise<void> {
        this.socket.end();
        return this.gone;
    }

    private receive(line: string): void {
        let message: unknown;
        try {
            message = JSON.parse(line);
        } catch {
            return;
        }
        switch (field(message, "type")) {
            case "role": {
                const pid = field(message, "pid");
                this.active = field(message, "active") === true;
                this.alone = field(message, "alone") === true;
                this.activePid = typeof pid === "number" ? pid : 0;
                this.events.role(this, this.active);
                break;
            }
            case "alone":
                this.alone = true;
                this.events.alone(this);
                break;
            case "overlap":
            case "missed":
                this.receiveSkipped(message);
                break;
            case "run":
                this.receiveRun(message);
                break;
            case "stop": {
                const run = field(message, "run");
                if (typeof run === "string") this.events.stop(run);
                break;
            }
            case "config_ok":
                this.replies.shift()?.({ ok: true });
                break;
            case "config_error": {
                const error = field(message, "error");
                this.replies.shift()?.({
                    ok: false,
                    error: typeof error === "string" ? error : "RunWisp rejected the config",
                    restart: field(message, "restart") === true,
                });
                break;
            }
            default:
            // Unknown types are ignored, so the daemon can be newer than the app.
        }
    }

    /** A firing RunWisp skipped (overlap) or missed while it was down. */
    private receiveSkipped(message: unknown): void {
        const task = field(message, "task");
        const at = field(message, "at");
        if (typeof task !== "string" || typeof at !== "string") return;
        if (field(message, "type") === "overlap") this.events.overlap(task, new Date(at));
        else this.events.missed(task, new Date(at));
    }

    private receiveRun(message: unknown): void {
        const run = field(message, "run");
        const task = field(message, "task");
        const at = field(message, "at");
        if (typeof run !== "string" || typeof task !== "string") return;
        this.events.run(this, {
            run,
            task,
            env: strings(field(message, "env")),
            params: strings(field(message, "params")),
            ...(typeof at === "string" ? { at: new Date(at) } : {}),
        });
    }
}
