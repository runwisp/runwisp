// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { mkdtempSync, rmSync } from "node:fs";
import { createServer, type Server, type Socket } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createInterface } from "node:readline";

import { afterEach, describe, expect, it } from "vitest";

import { Session, type RunRequest, type SessionEvents } from "./session.ts";

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
                    "HTTP/1.1 101 Switching Protocols\r\nUpgrade: runwisp-app\r\nConnection: Upgrade\r\n\r\n",
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

function recorder() {
    const runs: RunRequest[] = [];
    const roles: boolean[] = [];
    const events: SessionEvents = {
        role: (_s, active) => roles.push(active),
        run: (_s, request) => runs.push(request),
        stop: () => undefined,
        closed: () => undefined,
    };
    return { runs, roles, events };
}

describe("Session", () => {
    afterEach(() => {
        for (const server of servers.splice(0)) server.close();
        for (const dir of dirs.splice(0)) rmSync(dir, { recursive: true, force: true });
    });

    it("delivers a run that arrives together with the role", async () => {
        const { path } = await fakeDaemon((socket) => {
            socket.write(
                '{"type":"role","active":true}\n{"type":"run","run":"r1","task":"digest","env":{"A":"1"}}\n',
            );
        });
        const { runs, events } = recorder();
        const session = await Session.open(path, events);
        expect(session.active).toBe(true);
        await expect
            .poll(() => runs)
            .toEqual([{ run: "r1", task: "digest", env: { A: "1" }, params: {} }]);
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
        const session = await Session.open(path, recorder().events);
        expect(await session.config('{"tasks":{}}')).toEqual({ ok: true });
        expect(await session.config('{"daemon":"bad"}')).toEqual({
            ok: false,
            error: "tls_cert changed",
            restart: true,
        });
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
        const session = await Session.open(path, events);
        expect(roles).toEqual([false]);

        session.leave();
        await expect.poll(() => received).toEqual(['{"type":"close"}']);

        const pending = session.config("{}");
        daemonSide?.destroy();
        expect(await pending).toMatchObject({ ok: false, restart: false });
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
        await expect(Session.open(path, recorder().events)).rejects.toThrow("HTTP 403");
    });
});
