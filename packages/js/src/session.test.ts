// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { mkdtempSync, rmSync } from "node:fs";
import { createServer, type Server, type Socket } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createInterface } from "node:readline";

import { afterEach, describe, expect, it } from "vitest";

import { refusal, Session, type RunRequest, type SessionEvents } from "./session.ts";

/** A daemon's end of the app connection: answers the upgrade, then hands over the socket. */
async function fakeDaemon(
    onApp: (socket: Socket, received: string[]) => void,
): Promise<{ path: string; server: Server }> {
    const dir = mkdtempSync(join(tmpdir(), "rwn-"));
    dirs.push(dir);
    const path = join(dir, "runwisp.sock");
    const server = createServer((socket) => {
        let upgraded = false;
        const received: string[] = [];
        createInterface({ input: socket }).on("line", (line) => {
            if (upgraded) received.push(line);
            else if (line === "") {
                upgraded = true;
                socket.write(
                    "HTTP/1.1 101 Switching Protocols\r\nUpgrade: runwisp-app/1\r\nConnection: Upgrade\r\n\r\n",
                );
                onApp(socket, received);
            }
        });
    });
    servers.push(server);
    await new Promise<void>((resolve) => server.listen(path, resolve));
    return { path, server };
}

const dirs: string[] = [];
const servers: Server[] = [];
const WHO = { app: "shop", pid: 42 };

function recorder() {
    const runs: RunRequest[] = [];
    const roles: boolean[] = [];
    const overlaps: [string, Date][] = [];
    const missed: [string, Date][] = [];
    const events: SessionEvents = {
        role: (_s, active) => roles.push(active),
        alone: () => undefined,
        run: (_s, request) => runs.push(request),
        stop: () => undefined,
        overlap: (task, at) => overlaps.push([task, at]),
        missed: (task, at) => missed.push([task, at]),
        closed: () => undefined,
    };
    return { runs, roles, overlaps, missed, events };
}

describe("Session", () => {
    afterEach(() => {
        for (const server of servers.splice(0)) server.close();
        for (const dir of dirs.splice(0)) rmSync(dir, { recursive: true, force: true });
    });

    it("delivers a run that arrives together with the role", async () => {
        const { path } = await fakeDaemon((socket) => {
            socket.write(
                '{"type":"role","active":true}\n{"type":"run","run":"r1","task":"digest","env":{"A":"1"}}\n{"type":"run","run":"r2","task":"digest","at":"2026-01-02T03:04:00Z"}\n',
            );
        });
        const { runs, events } = recorder();
        const session = await Session.open(path, events, WHO);
        expect(session.active).toBe(true);
        await expect
            .poll(() => runs)
            .toEqual([
                { run: "r1", task: "digest", env: { A: "1" }, params: {} },
                {
                    run: "r2",
                    task: "digest",
                    env: {},
                    params: {},
                    at: new Date("2026-01-02T03:04:00Z"),
                },
            ]);
        await session.close();
    });

    it("pushes a config and maps the daemon's answer", async () => {
        const { path } = await fakeDaemon((socket, received) => {
            socket.write('{"type":"role","active":true}\n');
            const answer = () => {
                const last = received.at(-1) ?? "";
                if (last.includes('"bad"')) {
                    socket.write(
                        '{"type":"config_error","error":"tls_cert changed","restart":true}\n',
                    );
                } else socket.write('{"type":"config_ok","result":{}}\n');
            };
            socket.on("data", () => setImmediate(answer));
        });
        const session = await Session.open(path, recorder().events, WHO);
        expect(await session.config('{"tasks":{}}')).toEqual({ ok: true });
        expect(await session.config('{"daemon":"bad"}')).toEqual({
            ok: false,
            error: "tls_cert changed",
            restart: true,
        });
        await session.close();
    });

    it("refuses a second config while one waits for its answer, as replies carry no id", async () => {
        const { path } = await fakeDaemon((socket) => {
            socket.write('{"type":"role","active":true}\n');
            socket.on("data", () => setImmediate(() => socket.write('{"type":"config_ok"}\n')));
        });
        const session = await Session.open(path, recorder().events, WHO);
        const first = session.config("{}");
        expect(await session.config("{}")).toEqual({
            ok: false,
            error: "a config push is already pending",
            restart: false,
        });
        expect(await first).toEqual({ ok: true });
        await session.close();
    });

    it("says it is leaving, and fails a pending config when the daemon goes away", async () => {
        let daemonSide: Socket | undefined;
        let received: string[] = [];
        const { path } = await fakeDaemon((socket, lines) => {
            daemonSide = socket;
            received = lines;
            socket.write('{"type":"role","active":false}\n');
        });
        const { roles, events } = recorder();
        const session = await Session.open(path, events, WHO);
        expect(roles).toEqual([false]);

        session.leave();
        await expect.poll(() => received).toEqual(['{"type":"close"}']);

        const pending = session.config("{}");
        daemonSide?.destroy();
        expect(await pending).toMatchObject({ ok: false, restart: false });
    });

    it("names the app and its pid, and reads who is active, overlaps and missed runs", async () => {
        let request = "";
        const { path } = await fakeDaemon((socket) => {
            socket.write(
                '{"type":"role","active":false,"pid":7}\n{"type":"overlap","task":"digest","at":"2026-01-02T03:04:00Z"}\n{"type":"missed","task":"report","at":"2026-01-02T03:00:00Z"}\n',
            );
        });
        const server = servers.at(-1);
        server?.prependListener("connection", (socket: Socket) => {
            socket.once("data", (chunk: Buffer) => (request = chunk.toString()));
        });
        const { overlaps, missed, events } = recorder();
        const session = await Session.open(path, events, { app: "my app", pid: 42 });
        expect(request).toContain("GET /api/local/app?app=my+app&pid=42 HTTP/1.1");
        expect(request).toContain("Upgrade: runwisp-app/1");
        expect(session.activePid).toBe(7);
        await expect.poll(() => overlaps).toEqual([["digest", new Date("2026-01-02T03:04:00Z")]]);
        await expect.poll(() => missed).toEqual([["report", new Date("2026-01-02T03:00:00Z")]]);
        await session.close();
    });

    it("says why the daemon refused, from its answer", async () => {
        const dir = mkdtempSync(join(tmpdir(), "rwn-"));
        dirs.push(dir);
        const path = join(dir, "runwisp.sock");
        const body = 'another app ("billing", pid 11) uses this RunWisp daemon\n';
        // Kept open, as Go's server does: the app must not wait for it to close.
        const server = createServer((socket) => {
            socket.write(
                `HTTP/1.1 409 Conflict\r\nContent-Length: ${String(body.length)}\r\n\r\n${body}`,
            );
        });
        servers.push(server);
        await new Promise<void>((resolve) => server.listen(path, resolve));
        await expect(Session.open(path, recorder().events, WHO)).rejects.toThrow(
            'RunWisp refused the app connection (HTTP 409): another app ("billing", pid 11)',
        );
    });

    it("tells a runwisp binary that speaks another app protocol apart", () => {
        expect(refusal("426", "HTTP/1.1 426\r\nUpgrade: runwisp-app/2", "").message).toMatch(
            /^this runwisp binary is newer than @runwisp\/js \S+ supports .*install matching versions/,
        );
        expect(refusal("426", "HTTP/1.1 426\r\nUpgrade: runwisp-app/0", "").message).toContain(
            "is older than",
        );
    });

    it("rejects when the daemon refuses the upgrade", async () => {
        const dir = mkdtempSync(join(tmpdir(), "rwn-"));
        dirs.push(dir);
        const path = join(dir, "runwisp.sock");
        const server = createServer((socket) => {
            socket.end("HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n");
        });
        servers.push(server);
        await new Promise<void>((resolve) => server.listen(path, resolve));
        await expect(Session.open(path, recorder().events, WHO)).rejects.toThrow("HTTP 403");
    });
});
