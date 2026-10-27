// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// Talking to and starting the RunWisp binary. Everything here reuses what the
// `runwisp` CLI already does: the same flags, the same env vars, the same
// local socket API.

import { execFileSync, spawn } from "node:child_process";
import { createHash } from "node:crypto";
import {
    closeSync,
    existsSync,
    openSync,
    readFileSync,
    realpathSync,
    writeFileSync,
} from "node:fs";
import { lstat, mkdir } from "node:fs/promises";
import { request } from "node:http";
import { createServer } from "node:net";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import { dirname, isAbsolute, join, resolve } from "node:path";
import { setTimeout as sleep } from "node:timers/promises";

import type { Config } from "./generated/config.ts";
import type { RunWispOptions } from "./runwisp.ts";

export interface Settings {
    /** Names the app to the daemon: copies of one app share it, a different app is refused. */
    app: string;
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
    /** What the daemon this app started was started with (pid and fingerprint), to tell when it is stale. */
    launch: string;
}

export const DEFAULT_PORT = 9477;

const BUNDLED =
    "@runwisp/js was bundled into the app, so it can't find the runwisp binary: mark it external (Next.js: withRunWisp from @runwisp/js/next; Astro: the @runwisp/js/astro integration; other bundlers: their external option) and list it under dependencies, not devDependencies";

/** The `runwisp` npm package's launcher, else `runwisp` on PATH. */
export function resolveBinary(explicit: string | undefined): string {
    if (explicit) return explicit;
    let launcher: string;
    try {
        launcher = createRequire(import.meta.url).resolve("runwisp/bin/runwisp");
    } catch {
        return "runwisp";
    }
    // Turbopack, for one, swaps the resolve for a module id when it bundles this file.
    if (!isAbsolute(launcher)) throw new Error(BUNDLED);
    return launcher;
}

/** This package's package.json, or undefined when bundled into the app: `../package.json` is then the app's, or missing. */
function ownPackage(): unknown {
    try {
        const pkg: unknown = createRequire(import.meta.url)("../package.json");
        return field(pkg, "name") === "@runwisp/js" ? pkg : undefined;
    } catch {
        return undefined;
    }
}

/**
 * The runwisp binary's files on this platform, as absolute paths: what a
 * bundler that copies only the files it traces (Nitro, Next standalone)
 * must be told to copy. Empty without the runwisp npm package.
 */
export function binaryFiles(): string[] {
    let launcher: string;
    try {
        launcher = createRequire(import.meta.url).resolve("runwisp/bin/runwisp");
    } catch {
        return [];
    }
    const pkg = `@runwisp/${process.platform}-${process.arch}/runwisp`;
    // Where the launcher looks, and in isolated layouts (pnpm, bun --linker
    // isolated) the real file behind that symlink.
    const bins = [
        join(launcher, "../../node_modules", pkg),
        join(launcher, "../../..", pkg),
    ].filter((f) => existsSync(f));
    return [...new Set([launcher, ...bins, ...bins.map((f) => realpathSync(f))])];
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

/**
 * The app's name: the `name` in the nearest package.json from dir up, else
 * dir itself. Copies of one app agree on it, wherever each is deployed.
 */
export function defaultAppName(dir: string): string {
    for (let at = resolve(dir); ; at = dirname(at)) {
        try {
            const name = field(JSON.parse(readFileSync(join(at, "package.json"), "utf8")), "name");
            if (typeof name === "string" && name !== "") return name;
        } catch {
            // No package.json here, or one without a name: look further up.
        }
        if (dirname(at) === at) return resolve(dir);
    }
}

export function resolveSettings(
    options: Pick<
        RunWispOptions,
        "app" | "data" | "port" | "host" | "password" | "auth" | "binary"
    >,
): Settings {
    const envPort = Number(env("RUNWISP_PORT"));
    // Options win over the env: a password option turns the login on even with RUNWISP_AUTH=off.
    const auth = options.auth ?? (options.password !== undefined || env("RUNWISP_AUTH") !== "off");
    const password = options.password ?? (auth ? env("RUNWISP_PASSWORD") : undefined);
    if (!auth && password !== undefined) {
        throw new Error("password and auth: false can't be used together");
    }
    return {
        app: options.app ?? defaultAppName(process.cwd()),
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
        port: join(data, "app.port"),
        launch: join(data, "app.launch"),
    };
}

export function dashboardUrl(bindHost: string, port: number): string {
    const host = bindHost === "0.0.0.0" || bindHost === "::" ? "127.0.0.1" : bindHost;
    return `http://${host.includes(":") ? `[${host}]` : host}:${String(port)}`;
}

/** The port a previous start picked, from <data>/app.port. */
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
                // The daemon dying mid-answer must not leave the call hanging.
                const hungUp = (cause?: Error) => {
                    reject(new Error(`RunWisp hung up during ${method} ${path}`, { cause }));
                };
                res.on("error", hungUp);
                res.on("close", () => {
                    if (!res.complete) hungUp();
                });
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
    version: string;
    configPath: string;
    /** "app" when an app supplies the config, "file" for a runwisp.toml. */
    configSource: string;
}

export async function identity(socket: string): Promise<Identity> {
    const res = await call(socket, "GET", "/api/daemon/identity");
    const pid = field(res.body, "pid");
    const version = field(res.body, "version");
    const configPath = field(res.body, "configPath");
    const configSource = field(res.body, "configSource");
    if (
        res.status !== 200 ||
        typeof pid !== "number" ||
        typeof version !== "string" ||
        typeof configPath !== "string" ||
        typeof configSource !== "string"
    ) {
        throw new Error(`RunWisp identity: ${errorMessage(res)}`);
    }
    return { pid, version, configPath, configSource };
}

/** The state letter of a /proc/<pid>/stat line: "Z" for a zombie. */
export function procState(stat: string): string | undefined {
    // The command name, in parentheses, may itself hold ") ": the state follows the last one.
    return /^\) (\S)/.exec(stat.slice(stat.lastIndexOf(")")))?.[1];
}

function readProcStat(pid: number): string {
    return readFileSync(`/proc/${String(pid)}/stat`, "utf8");
}

/**
 * Whether pid runs. A zombie counts as gone: with node as PID 1 (Docker
 * without an init), an exited daemon is reparented to it and never reaped.
 * Where /proc is missing (macOS), kill(0) decides alone.
 */
export function processAlive(pid: number, readStat = readProcStat): boolean {
    try {
        process.kill(pid, 0);
    } catch (err) {
        return err instanceof Error && "code" in err && err.code === "EPERM";
    }
    let stat: string;
    try {
        stat = readStat(pid);
    } catch {
        return true;
    }
    return procState(stat) !== "Z";
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

/** The env a daemon this app starts runs with, and so do its shell tasks. */
function daemonEnv(settings: Settings): NodeJS.ProcessEnv {
    // settings already resolved these from the env; the inherited ones must not override them.
    const env: NodeJS.ProcessEnv = {
        ...process.env,
        RUNWISP_PASSWORD: undefined,
        RUNWISP_AUTH: undefined,
    };
    if (settings.password !== undefined) env.RUNWISP_PASSWORD = settings.password;
    if (!settings.auth) env.RUNWISP_AUTH = "off";
    return env;
}

/**
 * A hash of what a daemon started now with document would run with: the
 * binary path and the env it reads. That is the whole env when the config has
 * units that run in the daemon, whose shell sees it all. Else it is only what
 * RunWisp itself reads: RUNWISP_*, TZ and the ${VAR}s the config names, so
 * a variable that differs per copy, like PM2's NODE_APP_INSTANCE, doesn't make
 * the daemon stale. Holds no env value, so it is safe to store and log.
 */
export function launchFingerprint(settings: Settings, document: string): string {
    const named = new Set(Array.from(document.matchAll(/\$\{([^}:]+)\}/g), (m) => m[1]));
    const whole = runsInDaemon(document);
    const env = Object.entries(daemonEnv(settings))
        .filter((entry): entry is [string, string] => entry[1] !== undefined)
        .filter(
            ([name]) => whole || name.startsWith("RUNWISP_") || name === "TZ" || named.has(name),
        )
        .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0));
    return createHash("sha256")
        .update(JSON.stringify([settings.binary, env]))
        .digest("hex");
}

/** Whether document has a task or service that runs in the daemon rather than in the app. */
function runsInDaemon(document: string): boolean {
    const doc: unknown = JSON.parse(document);
    return ["tasks", "services"].some((kind) => {
        const units = field(doc, kind);
        return (
            typeof units === "object" &&
            !!units &&
            Object.values(units).some((unit) => field(unit, "sdk") !== true)
        );
    });
}

/** Notes what the daemon with pid was started with, for staleReason. */
export function recordLaunch(paths: DaemonPaths, pid: number, fingerprint: string): void {
    writeFileSync(paths.launch, JSON.stringify({ pid, fingerprint }), { mode: 0o600 });
}

/** This package's version, or undefined if it can't say. */
export function sdkVersion(): string | undefined {
    const version = field(ownPackage(), "version");
    return typeof version === "string" ? version : undefined;
}

/** The version `binary --version` reports, or undefined if it can't say. */
export function binaryVersion(binary: string): string | undefined {
    try {
        const out = execFileSync(binary, ["--version"], { encoding: "utf8", stdio: "pipe" });
        return /version (\S+)/.exec(out)?.[1];
    } catch {
        return undefined;
    }
}

/**
 * Why the running daemon is not what this app would start now, or undefined
 * when it is (or nothing says otherwise): another runwisp version, or for a
 * daemon this data dir's app started, another env or binary path. A daemon
 * outlives app restarts, so without this a redeploy with a rotated secret or
 * an upgraded runwisp would keep the old one.
 */
export function staleReason(
    settings: Settings,
    paths: DaemonPaths,
    daemon: Identity,
    document: string,
    version = binaryVersion(settings.binary),
): string | undefined {
    if (version !== undefined && version !== daemon.version) {
        return `it runs runwisp ${daemon.version} and ${settings.binary} is ${version}`;
    }
    let launch: unknown;
    try {
        launch = JSON.parse(readFileSync(paths.launch, "utf8"));
    } catch {
        return undefined;
    }
    if (field(launch, "pid") !== daemon.pid) return undefined;
    if (field(launch, "fingerprint") === launchFingerprint(settings, document)) return undefined;
    return "the app's environment or runwisp binary path changed since it started";
}

/**
 * A warning when binary (at version) is not the runwisp this package was
 * released with. Only another app protocol keeps them from working together,
 * so this is the one sign of a `binary` option or a `runwisp` on PATH left
 * behind by an upgrade. Dev builds (0.0.0, -dev) never warn.
 */
export function versionMismatch(
    binary: string,
    version: string | undefined,
    sdk = sdkVersion(),
): string | undefined {
    const unknown = (v: string | undefined) =>
        v === undefined || v === "0.0.0" || v.includes("-dev");
    if (unknown(version) || unknown(sdk) || version === sdk) return undefined;
    return `runwisp: ${binary} is runwisp ${String(version)} and @runwisp/js is ${String(sdk)}: install the same version of both`;
}

/**
 * Starts `runwisp daemon --app` with the app's config on its stdin, output in
 * <data>/daemon.log, and waits until it answers on its socket. Outside the
 * app's process tree: copies of the app share it, it outlives an app restart,
 * and it exits on its own once none is connected. Resolves to the pid it
 * started, which is not the one answering if another copy won a start race.
 */
export async function startDaemon(
    settings: Settings,
    paths: DaemonPaths,
    port: number,
    config: string,
): Promise<number> {
    const env = daemonEnv(settings);
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
    // The daemon must leave the app's process tree: watchers (nest start --watch)
    // kill every descendant by parent pid, and setsid alone keeps the parent. sh
    // starts it in the background, prints its pid and exits, so it is reparented
    // to init. A background job's stdin is /dev/null, so the config pipe goes
    // through fd 3.
    const child = spawn(
        "/bin/sh",
        ["-c", 'exec 3<&0; "$@" <&3 3<&- >&2 & echo $!', "sh", settings.binary, ...args],
        {
            env,
            stdio: ["pipe", "pipe", log],
            detached: true,
        },
    );
    closeSync(log);
    child.unref();
    // An error here (sh is missing) is reported by the child's own error event.
    child.stdin?.on("error", () => undefined);
    child.stdin?.end(config);
    let output = "";
    child.stdout?.on("data", (chunk: Buffer) => (output += chunk.toString()));
    const exited: { error?: Error } = {};
    child.once("error", (err) => (exited.error = err));

    const deadline = Date.now() + 15_000;
    const pid = () => Number(output.trim());
    while (Date.now() < deadline) {
        if (await healthy(paths.socket)) return pid();
        if (exited.error) throw new Error(`could not run /bin/sh: ${exited.error.message}`);
        // The daemon died at startup (a missing binary shows as sh's "not found" in the log).
        if (output.endsWith("\n") && !processAlive(pid())) break;
        await sleep(100);
    }
    // Lost a start race to another copy of the app: its daemon is fine to use.
    if (await healthy(paths.socket)) return pid();
    throw new Error(`RunWisp did not start (log: ${paths.log})\n${tail(paths.log, 20)}`);
}

/**
 * Runs a runwisp command that prints a JSON document, and returns it parsed,
 * also when the command exits non-zero (an invalid config or expression).
 * Throws when the binary can't answer at all.
 */
function runJson(binary: string, args: string[], input?: string): unknown {
    let output: string;
    let stderr = "";
    try {
        output = execFileSync(binary, args, {
            ...(input === undefined ? {} : { input }),
            encoding: "utf8",
            stdio: "pipe",
        });
    } catch (err) {
        const status = field(err, "status");
        if (typeof status !== "number" || status === 0) {
            const why =
                field(err, "code") !== "ENOENT"
                    ? String(err)
                    : ownPackage() !== undefined
                      ? "install the runwisp package"
                      : BUNDLED;
            throw new Error(`could not run ${binary}: ${why}`, { cause: err });
        }
        output = String(field(err, "stdout"));
        stderr = String(field(err, "stderr")).trim();
    }
    try {
        return JSON.parse(output);
    } catch {
        // An older runwisp without the command or flag, say.
        throw new Error(`could not run runwisp ${String(args[0])} with ${binary}: ${stderr}`);
    }
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
    const errors = field(
        runJson(
            binary,
            ["validate", "--app", "--json", "--config", paths.config],
            JSON.stringify(config),
        ),
        "errors",
    );
    if (!Array.isArray(errors)) throw new Error(`could not validate with ${binary}`);
    const messages = errors.map((e) => field(e, "message")).filter((m) => typeof m === "string");
    return messages.length > 0 ? messages.join("; ") : undefined;
}

/** What `runwisp sdk-cron --json` says about an expression. */
export interface CronAnswer {
    valid: boolean;
    /** Absent for an invalid expression, and for @every, which has no fields. */
    fields?: CronFields;
    errors: { field: string; value?: string; message: string }[];
    /** The next firings after the time asked about. */
    next: Date[];
}

export interface CronFields {
    second: number[];
    minute: number[];
    hour: number[];
    /** The plain days, then the specials as written: "L", "L-2", "LW", "15W". */
    dayOfMonth: (number | string)[];
    month: number[];
    /** The plain days, then the specials as written: "5#3", "5#L". */
    dayOfWeek: (number | string)[];
}

function numbers(value: unknown): number[] {
    return Array.isArray(value) ? value.filter((n) => typeof n === "number") : [];
}

function days(value: unknown): (number | string)[] {
    return Array.isArray(value)
        ? value.filter((n) => typeof n === "number" || typeof n === "string")
        : [];
}

/**
 * Asks `runwisp sdk-cron` about expression: whether it is valid, its fields, and
 * its next count firings after `after` in timezone (undefined: the host's,
 * which is the daemon's default), exactly as the daemon fires them.
 */
export function askCron(
    binary: string,
    expression: string,
    question: { timezone?: string | undefined; after?: Date; count: number },
): CronAnswer {
    const args = ["sdk-cron", "--json", "--count", String(question.count)];
    if (question.after) args.push("--after", question.after.toISOString());
    if (question.timezone !== undefined) args.push("--timezone", question.timezone);
    const doc = runJson(binary, [...args, "--", expression]);
    const valid = field(doc, "valid");
    const errors = field(doc, "errors");
    const next = field(doc, "next");
    if (typeof valid !== "boolean" || !Array.isArray(errors) || !Array.isArray(next)) {
        throw new Error(`could not read what ${binary} cron answered`);
    }
    const answer: CronAnswer = {
        valid,
        errors: errors.map((e) => {
            const value = field(e, "value");
            return {
                field: String(field(e, "field")),
                ...(typeof value === "string" ? { value } : {}),
                message: String(field(e, "message")),
            };
        }),
        next: next.filter((at) => typeof at === "string").map((at) => new Date(at)),
    };
    const fields = field(doc, "fields");
    if (fields !== undefined) {
        answer.fields = {
            second: numbers(field(fields, "second")),
            minute: numbers(field(fields, "minute")),
            hour: numbers(field(fields, "hour")),
            dayOfMonth: days(field(fields, "dayOfMonth")),
            month: numbers(field(fields, "month")),
            dayOfWeek: days(field(fields, "dayOfWeek")),
        };
    }
    return answer;
}
