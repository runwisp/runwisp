// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// The app's connection to the daemon: `GET /api/local/app` on its socket,
// upgraded to newline-delimited JSON. The messages mirror
// apps/runwisp/internal/apphost/host.go.

import { request } from "node:http";
import type { Socket } from "node:net";
import { createInterface } from "node:readline";

import { field } from "./daemon.ts";

const PROTOCOL = "runwisp-app";

/** A run of one of this app's tasks or services. */
export interface RunRequest {
    run: string;
    /** The task or service name. */
    task: string;
    /** The unit's env, secrets and env params, resolved by RunWisp. */
    env: Record<string, string>;
    params: Record<string, string>;
}

export type ConfigReply = { ok: true } | { ok: false; error: string; restart: boolean };

/** Each gets the session, which may arrive before open() resolves. */
export interface SessionEvents {
    /** Whether this copy of the app runs the tasks and supplies the config. Others stand by. */
    role(session: Session, active: boolean): void;
    run(session: Session, request: RunRequest): void;
    stop(run: string): void;
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

export class Session {
    active = false;
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

    /** Connects, and resolves once the daemon said whether this copy is active. */
    static open(socketPath: string, events: SessionEvents): Promise<Session> {
        return new Promise((resolve, reject) => {
            const req = request({
                socketPath,
                path: "/api/local/app",
                headers: { host: "runwisp", connection: "Upgrade", upgrade: PROTOCOL },
            });
            req.on("upgrade", (_res, socket, head) => {
                if (head.length > 0) socket.unshift(head);
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
            });
            req.on("response", (res) => {
                res.resume();
                reject(
                    new Error(
                        `RunWisp refused the app connection (HTTP ${String(res.statusCode)})`,
                    ),
                );
            });
            req.on("error", reject);
            req.end();
        });
    }

    private send(message: Record<string, unknown>): void {
        if (this.socket.writable) this.socket.write(JSON.stringify(message) + "\n");
    }

    output(run: string, stream: Stream, line: string): void {
        this.send({ type: "output", run, stream, line });
    }

    exit(run: string, code: number): void {
        this.send({ type: "exit", run, code });
    }

    /** Replaces the daemon's config with doc (JSON), as a reload would. One at a time. */
    config(doc: string): Promise<ConfigReply> {
        return new Promise((resolve) => {
            if (!this.socket.writable) {
                resolve({ ok: false, error: "RunWisp is not connected", restart: false });
                return;
            }
            this.replies.push(resolve);
            this.socket.write(`{"type":"config","doc":${doc}}\n`);
        });
    }

    /**
     * Says this copy is shutting down, not restarting. The next copy takes
     * over and this one hears it is on standby; when it was the last, RunWisp
     * stops instead, stopping its runs as it does at any shutdown.
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
        const run = field(message, "run");
        switch (field(message, "type")) {
            case "role":
                this.active = field(message, "active") === true;
                this.events.role(this, this.active);
                break;
            case "run": {
                const task = field(message, "task");
                if (typeof run !== "string" || typeof task !== "string") return;
                this.events.run(this, {
                    run,
                    task,
                    env: strings(field(message, "env")),
                    params: strings(field(message, "params")),
                });
                break;
            }
            case "stop":
                if (typeof run === "string") this.events.stop(run);
                break;
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
}
