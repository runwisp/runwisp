// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later
//
// check-installed-deps.ts — assert that what is actually installed in
// node_modules matches what every workspace manifest declares.
//
// Why this exists: `bun install` does not always prune per-workspace copies of a
// package when a version changes, so a workspace can keep resolving a stale one
// indefinitely. That is invisible — the manifest, the lockfile and `bun outdated`
// all agree, while the tool that runs is a different version. It bit us during a
// dependency sweep: every manifest pinned typescript 6.0.3 and the lockfile had a
// single 6.0.3 entry, but four workspaces still had a 5.9.2 directory left over,
// so type-checking ran under 5.9 while appearing to validate 6. CI installs into
// an empty tree and so never sees this; only local runs drift.
//
// The check is resolution-based on purpose: it asks "what does this workspace
// actually load?", the same question the toolchain asks, rather than trusting the
// lockfile.

import { existsSync, readFileSync, readdirSync } from "node:fs";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");

type Manifest = {
    name?: string;
    workspaces?: string[];
    dependencies?: Record<string, string>;
    devDependencies?: Record<string, string>;
    optionalDependencies?: Record<string, string>;
};

type Mismatch = {
    workspace: string;
    pkg: string;
    declared: string;
    installed: string;
    resolvedFrom: string;
};

function readManifest(path: string): Manifest {
    const parsed: unknown = JSON.parse(readFileSync(path, "utf8"));
    if (typeof parsed !== "object" || !parsed) throw new Error(`not an object: ${path}`);
    return parsed;
}

// Expands the single-level "apps/*" / "packages/*" globs the root manifest uses.
// Anything fancier is deliberately unsupported — it would only hide a typo.
function workspaceDirs(root: Manifest): string[] {
    const dirs: string[] = [repoRoot];

    for (const pattern of root.workspaces ?? []) {
        if (!pattern.endsWith("/*")) {
            throw new Error(`unsupported workspace pattern (want "<dir>/*"): ${pattern}`);
        }
        const parent = join(repoRoot, pattern.slice(0, -2));
        if (!existsSync(parent)) continue;

        for (const entry of readdirSync(parent, { withFileTypes: true })) {
            if (!entry.isDirectory()) continue;
            const dir = join(parent, entry.name);
            if (existsSync(join(dir, "package.json"))) dirs.push(dir);
        }
    }
    return dirs;
}

// Walks up from dir the way Node/Bun resolution does, returning the first
// node_modules/<pkg>/package.json that exists. Stops at the repo root: a hit
// outside it would not be what the toolchain loads either.
function resolveInstalled(dir: string, pkg: string): { version: string; from: string } | null {
    let current = dir;

    for (;;) {
        const candidate = join(current, "node_modules", pkg, "package.json");
        if (existsSync(candidate)) {
            const version = readManifest(candidate).version ?? "";
            return { version, from: relative(repoRoot, candidate) };
        }
        if (current === repoRoot) return null;
        const parent = dirname(current);
        if (parent === current) return null;
        current = parent;
    }
}

const root = readManifest(join(repoRoot, "package.json"));
const mismatches: Mismatch[] = [];
const missing: { workspace: string; pkg: string; declared: string }[] = [];
let checked = 0;

for (const dir of workspaceDirs(root)) {
    const manifest = readManifest(join(dir, "package.json"));
    const workspace = manifest.name ?? (relative(repoRoot, dir) || ".");
    const declared = {
        ...manifest.dependencies,
        ...manifest.devDependencies,
        ...manifest.optionalDependencies,
    };

    for (const [pkg, range] of Object.entries(declared)) {
        // workspace:* siblings are symlinked and carry their own version; npm:
        // and other protocols don't describe a plain semver range.
        if (!/^[\^~>=<\d*]/.test(range)) continue;

        checked++;
        const installed = resolveInstalled(dir, pkg);
        if (!installed) {
            missing.push({ workspace, pkg, declared: range });
            continue;
        }
        if (!Bun.semver.satisfies(installed.version, range)) {
            mismatches.push({
                workspace,
                pkg,
                declared: range,
                installed: installed.version,
                resolvedFrom: installed.from,
            });
        }
    }
}

if (!mismatches.length && !missing.length) {
    console.log(`check-installed-deps: ok — ${checked} declared dependencies resolve within range.`);
    process.exit(0);
}

for (const m of missing) {
    console.error(`${m.workspace}: ${m.pkg}@${m.declared} is declared but not installed`);
}
for (const m of mismatches) {
    console.error(
        `${m.workspace}: ${m.pkg} declares ${m.declared} but ${m.installed} is installed (${m.resolvedFrom})`,
    );
}
console.error(
    `\ncheck-installed-deps: ${missing.length + mismatches.length} problem(s) across ${checked} dependencies.\n` +
        `node_modules has drifted from the manifests. Re-sync with:\n` +
        `  find . -name node_modules -maxdepth 3 -type d -not -path '*/node_modules/*' -exec rm -rf {} + && bun install`,
);
process.exit(1);
