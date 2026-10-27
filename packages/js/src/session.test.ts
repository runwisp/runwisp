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
    const stops: string[] = [];
    const events: SessionEvents = {
        role: (_s, active) => roles.push(active),
        alone: () => undefined,
        run: (_s, request) => runs.push(request),
        stop: (run) => stops.push(`stop ${run}`),
        kill: (run) => stops.push(`kill ${run}`),
        overlap: (task, at) => overlaps.push([task, at]),
        missed: (task, at) => missed.push([task, at]),
        closed: () => undefined,
    };
    return { runs, roles, overlaps, missed, stops, events };
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
                } else socket.write('{"type":"config_ok","result":{"warnings":["w1",7]}}\n');
            };
            socket.on("data", () => setImmediate(answer));
        });
        const session = await Session.open(path, recorder().events, WHO);
        expect(await session.config('{"tasks":{}}')).toEqual({ ok: true, warnings: ["w1"] });
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
        expect(await first).toEqual({ ok: true, warnings: [] });
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
        await expect.poll(() => received).toEqual(['{"type":"leave"}']);

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

    it("names the running daemon that speaks another app protocol, and how to replace it", () => {
        const head = "HTTP/1.1 426\r\nUpgrade: runwisp-app/0";
        const message = refusal("426", head, "", { pid: 77, version: "1.6.0" }).message;
        expect(message).toMatch(
            /^RunWisp 1\.6\.0 \(pid 77\) speaks app protocol 0, and @runwisp\/js \S+ speaks runwisp-app\/1\./,
        );
        expect(message).toContain("stop it (kill 77)");
        expect(refusal("426", head, "").message).toMatch(/^The RunWisp running for this data/);
    });

    it("ignores what it can't read from the daemon and keeps going", async () => {
        const { path } = await fakeDaemon((socket) => {
            socket.write(
                [
                    '{"type":"role","active":"yes","pid":"7"}',
                    "[1]",
                    "null",
                    "not json",
                    '{"type":"run","run":5,"task":"digest"}',
                    '{"type":"missed","task":"digest","at":5}',
                    '{"type":"from-the-future","run":{}}',
                    '{"type":"run","run":"r1","task":"digest","env":{"A":1,"B":"2"},"params":[]}',
                ].join("\n") + "\n",
            );
        });
        const { runs, missed, events } = recorder();
        const session = await Session.open(path, events, WHO);
        expect(session.active).toBe(false);
        expect(session.activePid).toBe(0);
        await expect
            .poll(() => runs)
            .toEqual([{ run: "r1", task: "digest", env: { B: "2" }, params: {} }]);
        expect(missed).toEqual([]);
        await session.close();
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

    it("passes on stop and kill, and reports a skipped run's reason", async () => {
        let got: string[] = [];
        const { path } = await fakeDaemon((socket, received) => {
            got = received;
            socket.write(
                '{"type":"role","active":true}\n{"type":"stop","run":"r1"}\n{"type":"kill","run":"r1"}\n{"type":"kill"}\n',
            );
        });
        const { stops, events } = recorder();
        const session = await Session.open(path, events, WHO);
        await expect.poll(() => stops).toEqual(["stop r1", "kill r1"]);
        session.exit("r2", 1, "skipped");
        session.exit("r3", 0);
        await expect
            .poll(() => got)
            .toEqual([
                '{"type":"exit","run":"r2","code":1,"reason":"skipped"}',
                '{"type":"exit","run":"r3","code":0}',
            ]);
        await session.close();
    });

    it("drops output past the cap, keeps the connection, and says how much in the run's log", async () => {
        let daemon: Socket | undefined;
        let got: string[] = [];
        const { path } = await fakeDaemon((socket, received) => {
            daemon = socket;
            got = received;
            socket.write('{"type":"role","active":true}\n');
            // A daemon busy elsewhere: it reads nothing for now.
            socket.pause();
        });
        const cap = 4 * 1024 * 1024;
        const session = await Session.open(path, recorder().events, WHO, undefined, cap);
        const line = "x".repeat(1024);
        const sent = Math.ceil((cap * 1.5) / line.length);
        for (let i = 0; i < sent; i++) session.output("r1", "stdout", line);
        session.exit("r1", 0);
        daemon?.resume();

        const exit = '{"type":"exit","run":"r1","code":0}';
        await expect.poll(() => got.at(-1), { timeout: 10_000 }).toBe(exit);
        const kept = got.filter((m) => m.includes('"stdout"')).length;
        const notice = got.at(-2) ?? "";
        const dropped = Number(/dropped (\d+) lines/.exec(notice)?.[1]);
        expect(notice).toContain("the app wrote faster than RunWisp could take it");
        expect(kept).toBeLessThan(sent);
        expect(kept + dropped).toBe(sent);
        await session.close();
    });
});
