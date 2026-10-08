// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// Talking to and starting the RunWisp binary. Everything here reuses what the
// `runwisp` CLI already does: the same flags, the same env vars, the same
// local socket API.

import { execFileSync, spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { closeSync, openSync, readFileSync, writeFileSync } from "node:fs";
import { lstat, mkdir } from "node:fs/promises";
import { request } from "node:http";
import { createServer } from "node:net";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { setTimeout as sleep } from "node:timers/promises";

import type { Config } from "./generated/config.ts";
import type { RunWispOptions } from "./runwisp.ts";

export interface Settings {
    data: string;
    /** undefined: none was given, so RunWisp picks a free one (see choosePort). */
    port: number | undefined;
    host: string;
    password: string | undefined;
    auth: boolean;
    binary: string;
}

export interface DaemonPaths {
    /** Where runwisp.toml would be: the app's config stands in for it, and relative paths resolve against its directory. Never read. */
    config: string;
    socket: string;
    log: string;
    port: string;
}

export const DEFAULT_PORT = 9477;

/** The `runwisp` npm package's launcher, else `runwisp` on PATH. */
export function resolveBinary(explicit: string | undefined): string {
    if (explicit) return explicit;
    try {
        return createRequire(import.meta.url).resolve("runwisp/bin/runwisp");
    } catch {
        return "runwisp";
    }
}

/** An env var, treating empty as unset like the runwisp CLI does. */
function env(name: string): string | undefined {
    const value = process.env[name];
    return value === "" ? undefined : value;
}

/** RunWisp's own data-dir default (cmd/runwisp/root.go defaultDataDir). */
function defaultDataDir(): string {
    return env("RUNWISP_DATA") ?? (process.getuid?.() === 0 ? "/var/lib/runwisp" : ".runwisp");
}

export function resolveSettings(
    options: Pick<RunWispOptions, "data" | "port" | "host" | "password" | "auth" | "binary">,
): Settings {
    const envPort = Number(env("RUNWISP_PORT"));
    // Options win over the env: a password option turns the login on even with RUNWISP_AUTH=off.
    const auth = options.auth ?? (options.password !== undefined || env("RUNWISP_AUTH") !== "off");
    const password = options.password ?? (auth ? env("RUNWISP_PASSWORD") : undefined);
    if (!auth && password !== undefined) {
        throw new Error("password and auth: false can't be used together");
    }
    return {
        data: resolve(options.data ?? defaultDataDir()),
        port: options.port ?? (Number.isInteger(envPort) && envPort > 0 ? envPort : undefined),
        host: options.host ?? env("RUNWISP_HOST") ?? "127.0.0.1",
        password,
        auth,
        binary: resolveBinary(options.binary),
    };
}

// AF_UNIX paths are capped at 104 bytes on macOS (108 on Linux).
const MAX_SOCKET_PATH = 100;

function socketPath(data: string, file: string, label: string): string {
    const inData = join(data, file);
    if (Buffer.byteLength(inData) <= MAX_SOCKET_PATH) return inData;
    const hash = createHash("sha256").update(data).digest("hex").slice(0, 12);
    // A per-user directory: a shared /tmp would let another user bind the path first.
    return join(tmpdir(), `runwisp-${String(process.getuid?.() ?? 0)}`, `${label}-${hash}.sock`);
}

/** Creates the data dir, and the private socket dir when the socket path was too long for it. */
export async function prepareDirs(data: string, paths: DaemonPaths): Promise<void> {
    await mkdir(data, { recursive: true, mode: 0o700 });
    if (dirname(paths.socket) !== data) await privateDir(dirname(paths.socket));
}

async function privateDir(dir: string): Promise<void> {
    await mkdir(dir, { recursive: true, mode: 0o700 });
    const stat = await lstat(dir);
    const uid = process.getuid?.();
    if (
        !stat.isDirectory() ||
        (uid !== undefined && stat.uid !== uid) ||
        (stat.mode & 0o077) !== 0
    ) {
        throw new Error(`${dir} must be a directory that only you can access (mode 0700)`);
    }
}

export function daemonPaths(data: string): DaemonPaths {
    return {
        config: join(process.cwd(), "runwisp.toml"),
        socket: socketPath(data, "runwisp.sock", "daemon"),
        log: join(data, "daemon.log"),
        port: join(data, "node.port"),
    };
}

export function dashboardUrl(bindHost: string, port: number): string {
    const host = bindHost === "0.0.0.0" || bindHost === "::" ? "127.0.0.1" : bindHost;
    return `http://${host.includes(":") ? `[${host}]` : host}:${String(port)}`;
}

/** The port a previous start picked, from <data>/node.port. */
export function savedPort(paths: DaemonPaths): number | undefined {
    try {
        const port = Number(readFileSync(paths.port, "utf8"));
        return Number.isInteger(port) && port > 0 ? port : undefined;
    } catch {
        return undefined;
    }
}

function portFree(host: string, port: number): Promise<boolean> {
    return new Promise((resolvePromise) => {
        const server = createServer();
        server.once("error", () => {
            resolvePromise(false);
        });
        server.listen(port, host, () => {
            server.close(() => {
                resolvePromise(true);
            });
        });
    });
}

/** The first free port from `from` on, else `from` itself so RunWisp reports the conflict. */
export async function pickPort(host: string, from: number): Promise<number> {
    for (let port = from; port < from + 100; port++) {
        if (await portFree(host, port)) return port;
    }
    return from;
}

/**
 * The web UI port for a daemon about to start: the configured one, else the
 * one this data dir used before while it is free, else the first free port
 * from 9477. A picked port is saved so the URL stays the same across restarts.
 */
export async function choosePort(settings: Settings, paths: DaemonPaths): Promise<number> {
    if (settings.port !== undefined) return settings.port;
    const saved = savedPort(paths);
    if (saved !== undefined && (await portFree(settings.host, saved))) return saved;
    const port = await pickPort(settings.host, DEFAULT_PORT);
    writeFileSync(paths.port, String(port), { mode: 0o600 });
    return port;
}

export interface Response {
    status: number;
    body: unknown;
}

/**
 * One HTTP request to the daemon over its Unix socket (trusted local caller, no
 * login). Times out, so a wedged daemon can't hang the app.
 */
export function call(
    socket: string,
    method: string,
    path: string,
    timeoutMs = 10_000,
): Promise<Response> {
    return new Promise((resolvePromise, reject) => {
        const req = request(
            { socketPath: socket, method, path, headers: { host: "runwisp" } },
            (res) => {
                const chunks: Buffer[] = [];
                res.on("data", (chunk: Buffer) => chunks.push(chunk));
                res.on("end", () => {
                    const text = Buffer.concat(chunks).toString("utf8");
                    let body: unknown = text;
                    try {
                        body = JSON.parse(text);
                    } catch {
                        // Plain-text body (/health); keep the string.
                    }
                    resolvePromise({ status: res.statusCode ?? 0, body });
                });
            },
        );
        req.on("error", reject);
        req.setTimeout(timeoutMs, () => {
            req.destroy(new Error(`RunWisp did not answer ${method} ${path} in time`));
        });
        req.end();
    });
}

/** One property of a JSON body, or undefined. */
export function field(body: unknown, key: string): unknown {
    return typeof body === "object" && body !== null && key in body
        ? Object.getOwnPropertyDescriptor(body, key)?.value
        : undefined;
}

/** The message a RunWisp error response carries (huma: detail plus per-field errors). */
function errorMessage(res: Response): string {
    const detail = field(res.body, "detail");
    const errors = field(res.body, "errors");
    const parts = typeof detail === "string" ? [detail] : [`HTTP ${String(res.status)}`];
    if (Array.isArray(errors)) {
        for (const err of errors) {
            const message = field(err, "message");
            if (typeof message === "string") parts.push(message);
        }
    }
    return parts.join(": ");
}

export async function healthy(socket: string): Promise<boolean> {
    try {
        return (await call(socket, "GET", "/health", 2000)).status === 200;
    } catch {
        return false;
    }
}

export interface Identity {
    pid: number;
    configPath: string;
    /** "app" when an app supplies the config, "file" for a runwisp.toml. */
    configSource: string;
}

export async function identity(socket: string): Promise<Identity> {
    const res = await call(socket, "GET", "/api/daemon/identity");
    const pid = field(res.body, "pid");
    const configPath = field(res.body, "configPath");
    const configSource = field(res.body, "configSource");
    if (
        res.status !== 200 ||
        typeof pid !== "number" ||
        typeof configPath !== "string" ||
        typeof configSource !== "string"
    ) {
        throw new Error(`RunWisp identity: ${errorMessage(res)}`);
    }
    return { pid, configPath, configSource };
}

function processAlive(pid: number): boolean {
    try {
        process.kill(pid, 0);
        return true;
    } catch (err) {
        return err instanceof Error && "code" in err && err.code === "EPERM";
    }
}

/** Stops the daemon answering on socket, if any, and waits up to 10s for it to exit. */
export async function stopDaemon(socket: string): Promise<void> {
    let pid: number;
    try {
        ({ pid } = await identity(socket));
        process.kill(pid, "SIGTERM");
    } catch {
        return;
    }
    for (let waited = 0; waited < 10_000 && processAlive(pid); waited += 100) await sleep(100);
}

/** Next scheduled run per task, from GET /api/tasks. */
export async function nextRuns(socket: string): Promise<Map<string, Date>> {
    const res = await call(socket, "GET", "/api/tasks");
    const out = new Map<string, Date>();
    const items = field(res.body, "items");
    if (!Array.isArray(items)) return out;
    for (const item of items) {
        const name = field(item, "name");
        const next = field(item, "nextRunAt");
        if (typeof name === "string" && typeof next === "string") out.set(name, new Date(next));
    }
    return out;
}

function tail(path: string, lines: number): string {
    try {
        return readFileSync(path, "utf8").trimEnd().split("\n").slice(-lines).join("\n");
    } catch {
        return "";
    }
}

/**
 * Starts `runwisp daemon --app` with the app's config on its stdin, output in
 * <data>/daemon.log, and waits until it answers on its socket. Detached:
 * copies of the app share it, and it exits on its own once none is connected.
 */
export async function startDaemon(
    settings: Settings,
    paths: DaemonPaths,
    port: number,
    config: string,
): Promise<void> {
    // settings already resolved these from the env; the inherited ones must not override them.
    const env: NodeJS.ProcessEnv = {
        ...process.env,
        RUNWISP_PASSWORD: undefined,
        RUNWISP_AUTH: undefined,
    };
    if (settings.password !== undefined) env.RUNWISP_PASSWORD = settings.password;
    if (!settings.auth) env.RUNWISP_AUTH = "off";
    const args = [
        "daemon",
        "--app",
        "--config",
        paths.config,
        "--data",
        settings.data,
        "--socket",
        paths.socket,
        "--host",
        settings.host,
        "--port",
        String(port),
    ];
    const log = openSync(paths.log, "a", 0o600);
    const child = spawn(settings.binary, args, {
        env,
        stdio: ["pipe", log, log],
        detached: true,
    });
    closeSync(log);
    child.unref();
    // An error here (the binary is missing) is reported by the child's own error event.
    child.stdin?.on("error", () => undefined);
    child.stdin?.end(config);
    const exited: { code?: number | null; error?: Error } = {};
    child.once("exit", (code) => (exited.code = code));
    child.once("error", (err) => (exited.error = err));

    const deadline = Date.now() + 15_000;
    while (Date.now() < deadline) {
        if (await healthy(paths.socket)) return;
        if (exited.error)
            throw new Error(`could not run ${settings.binary}: ${exited.error.message}`);
        if (exited.code !== undefined) break;
        await sleep(100);
    }
    // Lost a start race to another copy of the app: its daemon is fine to use.
    if (await healthy(paths.socket)) return;
    throw new Error(`RunWisp did not start (log: ${paths.log})\n${tail(paths.log, 20)}`);
}

/**
 * Checks a config with `runwisp validate --app`, so the rules are exactly
 * RunWisp's. Returns RunWisp's error, or undefined when valid. Throws when the
 * binary can't validate at all.
 */
export function validateConfig(
    binary: string,
    paths: DaemonPaths,
    config: Config,
): string | undefined {
    let output: string;
    let stderr = "";
    try {
        output = execFileSync(binary, ["validate", "--app", "--json", "--config", paths.config], {
            input: JSON.stringify(config),
            encoding: "utf8",
            stdio: "pipe",
        });
    } catch (err) {
        const status = field(err, "status");
        if (typeof status !== "number" || status === 0) {
            const why =
                field(err, "code") === "ENOENT" ? "install the runwisp package" : String(err);
            throw new Error(`could not run ${binary}: ${why}`, { cause: err });
        }
        output = String(field(err, "stdout"));
        stderr = String(field(err, "stderr")).trim();
    }
    let errors: unknown;
    try {
        errors = field(JSON.parse(output), "errors");
    } catch {
        errors = undefined;
    }
    // An older runwisp without --app, say.
    if (!Array.isArray(errors)) throw new Error(`could not validate with ${binary}: ${stderr}`);
    const messages = errors.map((e) => field(e, "message")).filter((m) => typeof m === "string");
    return messages.length > 0 ? messages.join("; ") : undefined;
}

/** Checks a cron expression with `runwisp validate`, so the grammar is exactly RunWisp's. */
export function validateCron(binary: string, paths: DaemonPaths, expression: string): boolean {
    return (
        validateConfig(binary, paths, { tasks: { check: { cron: expression, run: "true" } } }) ===
        undefined
    );
}
