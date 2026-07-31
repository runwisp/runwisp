// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Converts bun.lock into a minimal SPDX 2.2 SBOM so CI can submit an accurate
// dependency snapshot via GitHub's Dependency Submission API. GitHub's static
// dependency graph doesn't parse bun.lock (only package-lock.json/yarn.lock/
// pnpm-lock.yaml) and npm's own CLI can't read this repo's manifests either
// (workspace:* is a bun/pnpm/yarn protocol npm doesn't understand), so this
// reads bun.lock's own resolved versions directly instead of routing through
// foreign tooling.

import { readFileSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";

const repoRoot = resolve(import.meta.dirname, "..");
const lockPath = resolve(repoRoot, "bun.lock");
const outPath = resolve(repoRoot, process.argv[2] ?? "bun.lock.spdx.json");

interface BunLock {
    packages: Record<string, [string, ...unknown[]]>;
}

// bun.lock is JSONC (trailing commas allowed); strip them before JSON.parse.
function parseJsonc(text: string): unknown {
    const withoutTrailingCommas = text.replace(/,(\s*[}\]])/g, "$1");
    return JSON.parse(withoutTrailingCommas);
}

function toPurl(name: string, version: string): string {
    const encoded = name.startsWith("@")
        ? `%40${encodeURIComponent(name.slice(1))}`
        : encodeURIComponent(name);
    return `pkg:npm/${encoded}@${encodeURIComponent(version)}`;
}

function toSpdxId(name: string, version: string): string {
    const safe = `${name}-${version}`.replace(/[^A-Za-z0-9.-]/g, "-");
    return `SPDXRef-Package-${safe}`;
}

const lock = parseJsonc(readFileSync(lockPath, "utf8")) as BunLock;

const seen = new Map<string, { name: string; version: string }>();
for (const entry of Object.values(lock.packages)) {
    const spec = entry[0];
    const at = spec.lastIndexOf("@");
    if (at <= 0) continue; // skip malformed/unversioned entries
    const name = spec.slice(0, at);
    const version = spec.slice(at + 1);
    if (version.startsWith("workspace:")) continue; // internal package, not a real published dependency
    seen.set(`${name}@${version}`, { name, version });
}

const packages = [...seen.values()]
    .sort((a, b) =>
        a.name === b.name
            ? a.version.localeCompare(b.version)
            : a.name.localeCompare(b.name),
    )
    .map(({ name, version }) => ({
        SPDXID: toSpdxId(name, version),
        name,
        versionInfo: version,
        downloadLocation: "NOASSERTION",
        licenseConcluded: "NOASSERTION",
        licenseDeclared: "NOASSERTION",
        copyrightText: "NOASSERTION",
        externalRefs: [
            {
                referenceCategory: "PACKAGE-MANAGER",
                referenceType: "purl",
                referenceLocator: toPurl(name, version),
            },
        ],
    }));

const documentId = "SPDXRef-DOCUMENT";
const sbom = {
    spdxVersion: "SPDX-2.2",
    dataLicense: "CC0-1.0",
    SPDXID: documentId,
    name: "runwisp-bun-dependencies",
    documentNamespace: `https://runwisp.com/spdx/${process.env.GITHUB_SHA ?? "local"}-${crypto.randomUUID()}`,
    creationInfo: {
        created: new Date().toISOString(),
        creators: ["Tool: bun-lock-to-spdx"],
    },
    packages,
    relationships: packages.map((pkg) => ({
        spdxElementId: documentId,
        relatedSpdxElement: pkg.SPDXID,
        relationshipType: "DEPENDS_ON",
    })),
};

writeFileSync(outPath, JSON.stringify(sbom, null, 2));
console.log(`Wrote ${packages.length} packages to ${outPath}`);
