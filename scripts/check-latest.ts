// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later
//
// check-latest.ts <app> — fail when a direct dependency of apps/<app> is behind
// its latest stable release. Go apps check go.mod's requires and toolchain line;
// JS apps check package.json deps against the versions locked in bun.lock.
//
// A release only counts once it is older than GRACE_SECONDS (default 1h), so a
// fresh tag doesn't turn every in-flight PR red the minute it's published.

import { existsSync, readFileSync } from "node:fs";
import { basename, dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

type Release = { version: string; time: string };
type Dep = { name: string; current: string; newer: Release[] };
type Json = Record<string, unknown>;

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const cutoff = Date.now() - Number(process.env.GRACE_SECONDS ?? 3600) * 1000;

// Releases we can't take yet, as a semver range the target must also satisfy,
// keyed "<pkg>" for every app or "<app>/<pkg>" for one. Drop an entry as soon
// as the reason is gone.
const holds: Record<string, string> = {
    // typescript-eslint, svelte-check, @astrojs/check and @sveltejs/kit all cap
    // their typescript peer below 7 (the native port).
    typescript: "<7",
    // SvelteKit 3 drops svelte.config.js, `$app/stores` and `$app/environment`,
    // and types `resolve()` strictly, so apps/ui and packages/ui need a
    // migration first. adapter-static 4 requires Kit 3.
    "@sveltejs/kit": "<3",
    "@sveltejs/adapter-static": "<4",
    // These two exist only to pin what a framework's build output imports:
    // SvelteKit's server chunk resolves cookie from apps/ui and needs its
    // named exports (gone in 1.x), Starlight's prerender chunk resolves js-yaml
    // from apps/docs and needs its default export (gone in 5).
    "ui/cookie": "<1",
    "docs/js-yaml": "<5",
};

const isJson = (x: unknown): x is Json =>
    !!x && typeof x === "object" && !Array.isArray(x);
const isStable = (v: string) => !v.includes("-");
const isNewer = (a: string, b: string) => Bun.semver.order(a, b) > 0;
const isDue = (r: Release) => Date.parse(r.time) <= cutoff;

function json(x: unknown, what: string): Json {
    if (!isJson(x)) throw new Error(`${what}: not an object`);
    return x;
}

async function fetchJson(url: string): Promise<Json> {
    const res = await fetch(url);
    if (!res.ok) throw new Error(`${url}: HTTP ${res.status}`);
    return json(await res.json(), url);
}

async function npmDeps(dir: string): Promise<Dep[]> {
    const manifest = json(
        JSON.parse(readFileSync(join(dir, "package.json"), "utf8")),
        "package.json",
    );
    const lock = json(
        Bun.JSONC.parse(readFileSync(join(repoRoot, "bun.lock"), "utf8")),
        "bun.lock",
    );
    const packages = json(lock.packages, "bun.lock packages");
    const declared = {
        ...json(manifest.dependencies ?? {}, "dependencies"),
        ...json(manifest.devDependencies ?? {}, "devDependencies"),
    };

    return Promise.all(
        Object.entries(declared)
            .filter(
                ([, range]) =>
                    typeof range === "string" &&
                    !range.startsWith("workspace:"),
            )
            .map(async ([name]) => {
                // bun.lock keys a workspace-specific resolution as "<workspace>/<pkg>".
                const entry =
                    packages[`${String(manifest.name)}/${name}`] ??
                    packages[name];
                const spec = Array.isArray(entry) ? entry[0] : undefined;
                if (typeof spec !== "string")
                    throw new Error(`${name} is not in bun.lock`);
                const current = spec.slice(spec.lastIndexOf("@") + 1);

                const meta = await fetchJson(
                    `https://registry.npmjs.org/${name}`,
                );
                const latest = json(
                    meta["dist-tags"],
                    `${name} dist-tags`,
                ).latest;
                if (typeof latest !== "string")
                    throw new Error(`${name} has no latest tag`);
                const newer = Object.entries(
                    json(meta.time, `${name} time`),
                ).flatMap(([version, time]) =>
                    typeof time === "string" &&
                    Bun.semver.satisfies(
                        version,
                        `>${current} <=${latest} ${holds[`${basename(dir)}/${name}`] ?? holds[name] ?? ""}`,
                    ) &&
                    isStable(version)
                        ? [{ version, time }]
                        : [],
                );
                return { name, current, newer };
            }),
    );
}

// proxy.golang.org case-encodes module paths: "BurntSushi" -> "!burnt!sushi".
const proxyPath = (path: string) =>
    path.replace(/[A-Z]/g, (c) => `!${c.toLowerCase()}`);

async function goDeps(dir: string): Promise<Dep[]> {
    const format =
        '{{if not (or .Main .Indirect)}}{{.Path}} {{.Version}}{{with .Update}} {{.Version}} {{.Time.Format "2006-01-02T15:04:05Z07:00"}}{{end}}{{end}}';
    const list = Bun.spawnSync(
        ["go", "list", "-m", "-u", "-f", format, "all"],
        { cwd: dir, stderr: "inherit" },
    );
    if (!list.success) throw new Error("go list -m -u failed");

    const deps = await Promise.all(
        list.stdout
            .toString()
            .split("\n")
            .filter(Boolean)
            .map(async (line) => {
                const [name = "", current = "", latest, time = ""] =
                    line.split(" ");
                if (!latest) return { name, current, newer: [] };
                const newer = [{ version: latest, time }];
                // The latest is still in its grace period, but an earlier release we also lack may not be.
                if (!isDue({ version: latest, time })) {
                    const base = `https://proxy.golang.org/${proxyPath(name)}/@v`;
                    const versions = (
                        await (await fetch(`${base}/list`)).text()
                    )
                        .split("\n")
                        .filter(Boolean);
                    for (const version of versions.filter(
                        (v) =>
                            isStable(v) &&
                            isNewer(v, current) &&
                            isNewer(latest, v),
                    )) {
                        const info = await fetchJson(`${base}/${version}.info`);
                        newer.push({ version, time: String(info.Time) });
                    }
                }
                return { name, current, newer };
            }),
    );

    const toolchain = /^toolchain go(\S+)$/m.exec(
        readFileSync(join(dir, "go.mod"), "utf8"),
    )?.[1];
    if (!toolchain) throw new Error("go.mod has no toolchain line");
    const releases: unknown = await fetch("https://go.dev/dl/?mode=json").then(
        (r) => r.json(),
    );
    if (!Array.isArray(releases)) throw new Error("go.dev/dl: not an array");
    const newer = await Promise.all(
        releases
            .map((r: unknown) =>
                String(json(r, "go.dev/dl release").version).replace(/^go/, ""),
            )
            .filter((v) => isNewer(v, toolchain))
            .map(async (version) => {
                // go.dev/dl has no dates; the toolchain module's proxy upload time is the release time.
                const info = await fetchJson(
                    `https://proxy.golang.org/golang.org/toolchain/@v/v0.0.1-go${version}.linux-amd64.info`,
                );
                return { version, time: String(info.Time) };
            }),
    );
    return [...deps, { name: "go toolchain", current: toolchain, newer }];
}

const app = process.argv[2];
if (!app) throw new Error("usage: bun scripts/check-latest.ts <app>");
const dir = join(repoRoot, "apps", app);
const deps = existsSync(join(dir, "go.mod"))
    ? await goDeps(dir)
    : await npmDeps(dir);

let stale = 0;
for (const { name, current, newer } of deps) {
    const due = newer
        .filter(isDue)
        .sort((a, b) => Bun.semver.order(b.version, a.version))[0];
    if (!due) continue;
    stale++;
    console.log(
        `::error::${name} ${due.version} is out (released ${due.time}), apps/${app} is on ${current}.`,
    );
}
console.log(
    stale
        ? `${stale} of ${deps.length} direct dependencies of apps/${app} are behind.`
        : `All ${deps.length} direct dependencies of apps/${app} are on their latest release.`,
);
process.exit(stale ? 1 : 0);
