// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0
//
// check-docs-links.ts — validate every docs-to-docs link in
// apps/docs/src/content/docs resolves to a real file and, when an anchor is
// present, to a real heading there. `/api/<operationId>` links are checked
// against openapi.json so the starlight-openapi generated routes are covered
// too. External links, image-only links, and asset references are out of scope.

import { existsSync, readFileSync, readdirSync, statSync } from "node:fs";
import { dirname, extname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import GithubSlugger from "github-slugger";

const here = dirname(fileURLToPath(import.meta.url));
const docsRoot = resolve(here, "../src/content/docs");
const repoRoot = resolve(here, "../../..");
const openapiCandidates = [
    resolve(here, "../public/openapi.json"),
    resolve(repoRoot, "apps/runwisp/openapi.json"),
];

type Finding = {
    file: string;
    line: number;
    target: string;
    reason: string;
};

type OpenApiIndex = {
    operationIds: Set<string>;
    operationIdSlugs: Set<string>;
};

type LinkMatch = {
    target: string;
    line: number;
    index: number;
};

type DocIndex = {
    anchorsByFile: Map<string, Set<string>>;
};

function walk(dir: string): string[] {
    const out: string[] = [];
    for (const entry of readdirSync(dir)) {
        const full = join(dir, entry);
        const s = statSync(full);
        if (s.isDirectory()) {
            out.push(...walk(full));
        } else if (/\.(md|mdx)$/.test(entry)) {
            out.push(full);
        }
    }
    return out;
}

function stripFrontmatter(src: string): string {
    if (!src.startsWith("---")) return src;
    const end = src.indexOf("\n---", 3);
    if (end < 0) return src;
    return src.slice(end + 4);
}

function getFrontmatterField(src: string, field: string): string | undefined {
    if (!src.startsWith("---")) return undefined;
    const end = src.indexOf("\n---", 3);
    if (end < 0) return undefined;
    const block = src.slice(0, end);
    const re = new RegExp(String.raw`^${field}:\s*(.+?)\s*$`, "m");
    const m = re.exec(block);
    if (!m) return undefined;
    const raw = m[1];
    if (raw.startsWith('"') && raw.endsWith('"')) return raw.slice(1, -1);
    if (raw.startsWith("'") && raw.endsWith("'")) return raw.slice(1, -1);
    return raw;
}

// Match inline markdown link destinations: [text](destination). Image links
// `![alt](...)` are filtered out by the negative lookbehind. Reference-style
// links are out of scope for this check — the corpus uses inline links.
// The destination is matched atomically (lookahead-capture + backreference, the
// `\2`) so a `(` without a closing `)` fails fast instead of backtracking
// through every destination length (super-linear). Group 2 stays the dest.
const linkRe = /(?<!!)\[([^\]\n]*)\]\((?=([^)\s]+))\2(?:\s+"[^"]*")?\)/g;

function buildLineOffsets(src: string): number[] {
    const offsets: number[] = [];
    let line = 1;
    for (let i = 0; i < src.length; i++) {
        offsets.push(line);
        if (src.codePointAt(i) === 10) line++;
    }
    offsets.push(line);
    return offsets;
}

function extractLinks(src: string): LinkMatch[] {
    const offsets = buildLineOffsets(src);
    const out: LinkMatch[] = [];
    for (const m of src.matchAll(linkRe)) {
        const idx = m.index;
        const line = offsets[idx] ?? 1;
        out.push({ target: m[2], line, index: idx });
    }
    return out;
}

// Capture the heading text greedily to end-of-line (no ambiguous trailing
// `\s*#*\s*` quantifiers, which backtrack super-linearly); ATX-style closing
// hashes are stripped afterwards in code.
const headingRe = /^(#{1,6})\s+(.+)$/gm;

// Strip an ATX-style closing hash run (`## Title ##`) and surrounding space.
// Single quantifiers anchored to the end — no backtracking.
function stripAtxClosing(text: string): string {
    return text.trimEnd().replace(/#+$/, "").trimEnd();
}

function collectAnchors(stripped: string, frontmatter: string): Set<string> {
    const slugger = new GithubSlugger();
    const out = new Set<string>();
    const title = getFrontmatterField(frontmatter, "title");
    if (title) out.add(slugger.slug(title));
    for (const m of stripped.matchAll(headingRe)) {
        out.add(slugger.slug(stripAtxClosing(m[2])));
    }
    return out;
}

function buildIndex(files: string[]): DocIndex {
    const anchorsByFile = new Map<string, Set<string>>();
    for (const f of files) {
        const src = readFileSync(f, "utf8");
        const stripped = stripFrontmatter(src);
        anchorsByFile.set(f, collectAnchors(stripped, src));
    }
    return { anchorsByFile };
}

function findDocFile(relPath: string): string | undefined {
    if (extname(relPath) === "") {
        for (const ext of [".mdx", ".md"]) {
            const cand = resolve(docsRoot, relPath + ext);
            if (existsSync(cand) && statSync(cand).isFile()) return cand;
        }
        return undefined;
    }
    const cand = resolve(docsRoot, relPath);
    if (existsSync(cand) && statSync(cand).isFile()) return cand;
    return undefined;
}

function resolveDocTarget(target: string, fromFile: string): string | undefined {
    const hash = target.indexOf("#");
    const raw = hash >= 0 ? target.slice(0, hash) : target;
    if (raw === "") return fromFile;
    let rel: string;
    if (raw.startsWith("/")) rel = raw.slice(1);
    else rel = relative(docsRoot, resolve(dirname(fromFile), raw));
    return findDocFile(rel);
}

function collectOpenApiIds(
    paths: Record<string, Record<string, { operationId?: unknown }>>,
): OpenApiIndex {
    const ids = new Set<string>();
    const slugs = new Set<string>();
    const slugger = new GithubSlugger();
    for (const methods of Object.values(paths)) {
        for (const op of Object.values(methods)) {
            if (typeof op.operationId === "string") {
                ids.add(op.operationId);
                slugs.add(slugger.slug(op.operationId));
            }
        }
    }
    return { operationIds: ids, operationIdSlugs: slugs };
}

function loadOpenApi(): OpenApiIndex | undefined {
    for (const candidate of openapiCandidates) {
        if (!existsSync(candidate)) continue;
        const raw: unknown = JSON.parse(readFileSync(candidate, "utf8"));
        if (!isOpenApiDoc(raw)) return undefined;
        return collectOpenApiIds(raw.paths);
    }
    return undefined;
}

function isOpenApiDoc(value: unknown): value is {
    paths: Record<string, Record<string, { operationId?: unknown }>>;
} {
    if (typeof value !== "object" || value === null) return false;
    const v: { paths?: unknown } = value;
    if (typeof v.paths !== "object" || v.paths === null) return false;
    return true;
}

function isExternal(target: string): boolean {
    return /^(https?:|mailto:|tel:|ftp:|sms:|data:)/i.test(target);
}

function isAnchorOnly(target: string): boolean {
    return target.startsWith("#");
}

function isApiLink(path: string): boolean {
    return path === "/api" || path.startsWith("/api/");
}

function stripHash(raw: string): string {
    const hash = raw.indexOf("#");
    return hash >= 0 ? raw.slice(0, hash) : raw;
}

function isAssetLink(target: string, fromFile: string): boolean {
    if (isExternal(target) || isAnchorOnly(target) || isApiLink(stripHash(target))) {
        return false;
    }
    const raw = stripHash(target);
    if (raw === "") return false;
    const resolved = raw.startsWith("/")
        ? resolve(docsRoot, "." + raw)
        : resolve(dirname(fromFile), raw);
    if (existsSync(resolved)) return !/\.(md|mdx)$/.test(resolved);
    return !/\.(md|mdx)$/.test(raw);
}

function checkApiLink(pathPart: string, openapi: OpenApiIndex | undefined): string | undefined {
    if (!openapi) {
        return "openapi.json not found; cannot validate /api/ link";
    }
    const opId = pathPart.replace(/^\/api\/?/, "").replace(/\/$/, "");
    if (opId === "") return undefined;
    if (openapi.operationIds.has(opId) || openapi.operationIdSlugs.has(opId)) {
        return undefined;
    }
    return `operation '${opId}' not found in openapi.json`;
}

function checkDocLink(
    pathPart: string,
    fragPart: string,
    fromFile: string,
    index: DocIndex,
): string | undefined {
    const dest = resolveDocTarget(pathPart, fromFile);
    if (!dest) return "destination file does not exist";
    if (!fragPart) return undefined;
    const anchors = index.anchorsByFile.get(dest);
    if (anchors?.has(fragPart) === true) return undefined;
    return `anchor '#${fragPart}' not found in destination`;
}

function partitionTarget(target: string): { path: string; fragment: string } {
    const hashIdx = target.indexOf("#");
    if (hashIdx < 0) return { path: target, fragment: "" };
    return { path: target.slice(0, hashIdx), fragment: target.slice(hashIdx + 1) };
}

function checkLink(
    link: LinkMatch,
    fromFile: string,
    index: DocIndex,
    openapi: OpenApiIndex | undefined,
): Finding | undefined {
    const { target, line } = link;
    if (isExternal(target)) return undefined;
    if (isAssetLink(target, fromFile)) return undefined;
    const { path, fragment } = partitionTarget(target);
    let reason: string | undefined;
    if (isApiLink(path)) reason = checkApiLink(path, openapi);
    else reason = checkDocLink(path, fragment, fromFile, index);
    if (reason === undefined) return undefined;
    return { file: relative(repoRoot, fromFile), line, target, reason };
}

function report(findings: Finding[], totalFiles: number): number {
    findings.sort((a, b) => (a.file === b.file ? a.line - b.line : a.file.localeCompare(b.file)));
    if (findings.length === 0) {
        console.log(
            `check-docs-links: ok — checked ${String(totalFiles)} files, no broken docs-to-docs links.`,
        );
        return 0;
    }
    let currentFile = "";
    for (const f of findings) {
        if (f.file !== currentFile) {
            currentFile = f.file;
            console.log(`\n${f.file}`);
        }
        console.log(`  ${String(f.line)}: [${f.target}] ${f.reason}`);
    }
    const fileCount = new Set(findings.map((f) => f.file)).size;
    console.log(
        `\ncheck-docs-links: ${String(findings.length)} broken link(s) across ${String(fileCount)} file(s) (of ${String(totalFiles)} checked).`,
    );
    return 1;
}

function main(): number {
    if (!existsSync(docsRoot)) {
        console.error(`check-docs-links: docs root not found at ${docsRoot}`);
        return 2;
    }
    const files = walk(docsRoot);
    const index = buildIndex(files);
    const openapi = loadOpenApi();
    const findings: Finding[] = [];
    for (const f of files) {
        const src = readFileSync(f, "utf8");
        for (const link of extractLinks(src)) {
            const finding = checkLink(link, f, index, openapi);
            if (finding !== undefined) findings.push(finding);
        }
    }
    return report(findings, files.length);
}

process.exit(main());
