// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { chmodSync, mkdtempSync, readFileSync, rmSync, statSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";

import { afterEach, describe, expect, it, vi } from "vitest";

import {
    call,
    choosePort,
    daemonPaths,
    pickPort,
    prepareDirs,
    resolveSettings,
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
