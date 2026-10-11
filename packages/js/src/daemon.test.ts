// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import {
    chmodSync,
    mkdirSync,
    mkdtempSync,
    readFileSync,
    rmSync,
    statSync,
    writeFileSync,
} from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";

import { afterEach, describe, expect, it, vi } from "vitest";

import {
    call,
    choosePort,
    daemonPaths,
    defaultAppName,
    launchFingerprint,
    pickPort,
    prepareDirs,
    processAlive,
    procState,
    recordLaunch,
    resolveSettings,
    staleReason,
    validateConfig,
    type Settings,
} from "./daemon.ts";

describe("settings", () => {
    afterEach(() => {
        vi.unstubAllEnvs();
    });

    it("lets options override RUNWISP_AUTH and RUNWISP_PASSWORD", () => {
        vi.stubEnv("RUNWISP_AUTH", "off");
        vi.stubEnv("RUNWISP_PASSWORD", "from-env");
        expect(resolveSettings({})).toMatchObject({ auth: false, password: undefined });
        expect(resolveSettings({ auth: true })).toMatchObject({ auth: true, password: "from-env" });
        expect(resolveSettings({ password: "x" })).toMatchObject({ auth: true, password: "x" });
        expect(() => resolveSettings({ auth: false, password: "x" })).toThrow(
            "can't be used together",
        );
    });
});

describe("daemon calls", () => {
    it("time out when the daemon accepts but never answers", async () => {
        const dir = mkdtempSync(join(tmpdir(), "rwn-"));
        const socket = join(dir, "wedged.sock");
        const server = createServer(() => undefined);
        await new Promise<void>((resolve) => server.listen(socket, resolve));
        try {
            await expect(call(socket, "GET", "/health", 100)).rejects.toThrow("did not answer");
        } finally {
            server.close();
            rmSync(dir, { recursive: true, force: true });
        }
    });

    it("fail when the daemon hangs up in the middle of an answer", async () => {
        const dir = mkdtempSync(join(tmpdir(), "rwn-"));
        const socket = join(dir, "dying.sock");
        const server = createServer((conn) => {
            conn.write('HTTP/1.1 200 OK\r\nContent-Length: 100\r\n\r\n{"items":');
            setTimeout(() => conn.destroy(), 10);
        });
        await new Promise<void>((resolve) => server.listen(socket, resolve));
        try {
            await expect(call(socket, "GET", "/api/tasks", 60_000)).rejects.toThrow(
                "hung up during GET /api/tasks",
            );
        } finally {
            server.close();
            rmSync(dir, { recursive: true, force: true });
        }
    });

    it("tells a missing binary apart from an invalid config", () => {
        expect(() => validateConfig("/nonexistent/runwisp", daemonPaths("/tmp"), {})).toThrow(
            "install the runwisp package",
        );
    });
});

describe("socket paths", () => {
    const dirs: string[] = [];
    afterEach(() => {
        for (const dir of dirs.splice(0)) rmSync(dir, { recursive: true, force: true });
    });

    it("keeps the socket in the data dir when the path fits", () => {
        expect(daemonPaths("/srv/app/.runwisp").socket).toBe("/srv/app/.runwisp/runwisp.sock");
    });

    it("moves a too-long socket path into a private per-user dir", async () => {
        const root = mkdtempSync(join(tmpdir(), "rwn-"));
        dirs.push(root);
        const data = join(root, "x".repeat(100));
        const paths = daemonPaths(data);
        const socketDir = dirname(paths.socket);
        expect(socketDir).toBe(join(tmpdir(), `runwisp-${String(process.getuid?.())}`));

        await prepareDirs(data, paths);
        expect(statSync(socketDir).mode & 0o777).toBe(0o700);

        chmodSync(socketDir, 0o777);
        await expect(prepareDirs(data, paths)).rejects.toThrow("only you can access");
        chmodSync(socketDir, 0o700);
    });
});

describe("port", () => {
    it("skips a port that is taken", async () => {
        const server = createServer();
        await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
        const address = server.address();
        const taken = typeof address === "object" && address ? address.port : 0;
        try {
            expect(await pickPort("127.0.0.1", taken)).toBeGreaterThan(taken);
        } finally {
            server.close();
        }
    });

    it("remembers the port it picked, and keeps an explicit one", async () => {
        const data = mkdtempSync(join(tmpdir(), "rwn-"));
        try {
            const paths = daemonPaths(data);
            const settings: Settings = {
                app: "app",
                data,
                port: undefined,
                host: "127.0.0.1",
                password: undefined,
                auth: true,
                binary: "runwisp",
            };
            const port = await choosePort(settings, paths);
            expect(readFileSync(paths.port, "utf8")).toBe(String(port));
            expect(await choosePort(settings, paths)).toBe(port);
            expect(await choosePort({ ...settings, port: 1234 }, paths)).toBe(1234);
        } finally {
            rmSync(data, { recursive: true, force: true });
        }
    });
});

describe("processes", () => {
    it("reads the state from a /proc stat line, whatever the command name holds", () => {
        expect(procState("42 (node) S 1 42 42 0 -1")).toBe("S");
        expect(procState("42 (we) ird) Z 1 42")).toBe("Z");
        expect(procState("")).toBeUndefined();
    });

    // node as PID 1 in Docker never reaps the daemon it reparented.
    it("counts a zombie as gone", () => {
        expect(processAlive(process.pid, () => `${String(process.pid)} (runwisp) Z 1`)).toBe(false);
        expect(processAlive(process.pid, () => `${String(process.pid)} (node) R 1`)).toBe(true);
        expect(
            processAlive(process.pid, () => {
                throw new Error("no /proc here");
            }),
        ).toBe(true);
        expect(processAlive(2 ** 30)).toBe(false);
    });
});

describe("app name", () => {
    it("is the nearest package.json name, else the directory", () => {
        const root = mkdtempSync(join(tmpdir(), "rwn-"));
        try {
            const app = join(root, "app");
            const nested = join(app, "src", "jobs");
            mkdirSync(nested, { recursive: true });
            writeFileSync(join(app, "package.json"), JSON.stringify({ name: "billing" }));
            expect(defaultAppName(nested)).toBe("billing");
            writeFileSync(join(nested, "package.json"), JSON.stringify({ private: true }));
            expect(defaultAppName(nested)).toBe("billing");
            expect(defaultAppName(root)).toBe(root);
        } finally {
            rmSync(root, { recursive: true, force: true });
        }
    });
});

describe("stale daemon", () => {
    afterEach(() => {
        vi.unstubAllEnvs();
    });

    function setup() {
        const data = mkdtempSync(join(tmpdir(), "rwn-"));
        const binary = join(data, "runwisp");
        writeFileSync(binary, '#!/bin/sh\necho "runwisp version 1.2.0"\n');
        chmodSync(binary, 0o755);
        const settings: Settings = {
            ...resolveSettings({ data, binary, app: "app" }),
        };
        const daemon = { pid: 1234, version: "1.2.0", configPath: "", configSource: "app" };
        return { data, settings, paths: daemonPaths(data), daemon };
    }

    // A shell task sees the daemon's whole env; an sdk one runs in the app.
    const SHELL = JSON.stringify({ tasks: { backup: { run: "./backup.sh" } } });
    const SDK = JSON.stringify({ tasks: { nightly: { sdk: true } } });

    it("is fresh when started with this env and binary", () => {
        const { data, settings, paths, daemon } = setup();
        try {
            recordLaunch(paths, daemon.pid, launchFingerprint(settings, SHELL));
            expect(staleReason(settings, paths, daemon, SHELL)).toBeUndefined();
            // Unknown: nothing recorded, or recorded for another daemon.
            rmSync(paths.launch);
            expect(staleReason(settings, paths, daemon, SHELL)).toBeUndefined();
            recordLaunch(paths, 999, "other");
            expect(staleReason(settings, paths, daemon, SHELL)).toBeUndefined();
        } finally {
            rmSync(data, { recursive: true, force: true });
        }
    });

    it("is stale after the env changed, without holding the env's values", () => {
        const { data, settings, paths, daemon } = setup();
        try {
            vi.stubEnv("API_SECRET", "old-secret");
            recordLaunch(paths, daemon.pid, launchFingerprint(settings, SHELL));
            expect(readFileSync(paths.launch, "utf8")).not.toContain("old-secret");
            vi.stubEnv("API_SECRET", "rotated");
            expect(staleReason(settings, paths, daemon, SHELL)).toContain("environment");
        } finally {
            rmSync(data, { recursive: true, force: true });
        }
    });

    // PM2 cluster mode gives each copy its own NODE_APP_INSTANCE.
    it("ignores env RunWisp doesn't read when every unit runs in the app", () => {
        const { data, settings, paths, daemon } = setup();
        try {
            vi.stubEnv("NODE_APP_INSTANCE", "0");
            recordLaunch(paths, daemon.pid, launchFingerprint(settings, SDK));
            vi.stubEnv("NODE_APP_INSTANCE", "1");
            expect(staleReason(settings, paths, daemon, SDK)).toBeUndefined();

            const named = JSON.stringify({
                tasks: { nightly: { sdk: true, env: { TOKEN: "${API_SECRET}" } } },
            });
            vi.stubEnv("API_SECRET", "old-secret");
            recordLaunch(paths, daemon.pid, launchFingerprint(settings, named));
            vi.stubEnv("API_SECRET", "rotated");
            expect(staleReason(settings, paths, daemon, named)).toContain("environment");
        } finally {
            rmSync(data, { recursive: true, force: true });
        }
    });

    it("is stale when it runs another runwisp version", () => {
        const { data, settings, paths, daemon } = setup();
        try {
            expect(staleReason(settings, paths, { ...daemon, version: "1.1.0" }, SHELL)).toBe(
                `it runs runwisp 1.1.0 and ${settings.binary} is 1.2.0`,
            );
        } finally {
            rmSync(data, { recursive: true, force: true });
        }
    });
});
