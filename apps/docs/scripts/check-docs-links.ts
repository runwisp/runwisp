// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later
//
// check-docs-links.ts — validate every link this repo controls that points into
// the docs site.
//
// Two link sources are checked against one page+anchor index:
//
//   1. Docs-internal links in apps/docs/src/content/docs — both markdown
//      `[text](/path/)` and JSX `href="/path/"` (LinkCard/Card components),
//      including same-page `#anchor` links.
//   2. Outbound `https://docs.runwisp.com/...` URLs elsewhere in the repo —
//      README.md, SECURITY.md, scripts/install.sh, the Go sources (the daemon
//      prints docs URLs, and scaffolded runwisp.toml embeds them), and the
//      config JSON Schema. CHANGELOG.md is deliberately excluded: it is a
//      historical record of released text and must not be rewritten to chase
//      moved pages.
//
// `/api/<operationId>` links are checked against openapi.json so the
// starlight-openapi generated routes are covered too.
//
// Redirects declared in astro.config.mjs are resolved, but a link that only
// works *via* a redirect is an error: redirects exist for URLs already shipped
// in released binaries and CHANGELOG entries, which this script does not scan.
// Anything in a file we control should point at the live URL.

import { existsSync, readFileSync, readdirSync, statSync } from "node:fs";
import { dirname, extname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import GithubSlugger from "github-slugger";

const here = dirname(fileURLToPath(import.meta.url));
const docsRoot = resolve(here, "../src/content/docs");
const repoRoot = resolve(here, "../../..");
const astroConfigPath = resolve(here, "../astro.config.mjs");
const openapiCandidates = [
    resolve(here, "../public/openapi.json"),
    resolve(repoRoot, "apps/runwisp/openapi.json"),
];

const SITE = "https://docs.runwisp.com";

// Files outside apps/docs that embed absolute docs URLs. Directories are walked
// for the listed extensions. CHANGELOG.md is intentionally absent.
const outboundRoots: ReadonlyArray<{ path: string; exts?: ReadonlyArray<string> }> = [
    { path: "README.md" },
    { path: "SECURITY.md" },
    { path: "scripts/install.sh" },
    { path: "apps/runwisp", exts: [".go", ".json"] },
];

// Routes served by src/pages/*, not by the docs content collection. They have no
// entry in the page index, so they are accepted as-is.
//   /agents/reference(.md) — src/pages/agents/reference.md.ts
//   /llms.txt             — src/pages/llms.txt.ts
//   /config.schema.json, /openapi.json — copied into public/ by sync-openapi.ts
const generatedRoutes = new Set([
    "/agents/reference",
    "/agents/reference.md",
    "/llms.txt",
    "/config.schema.json",
    "/openapi.json",
]);

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
};

type DocIndex = {
    anchorsByFile: Map<string, Set<string>>;
};

function walk(dir: string, exts: ReadonlyArray<string> = [".md", ".mdx"]): string[] {
    const out: string[] = [];
    for (const entry of readdirSync(dir)) {
        if (entry === "node_modules") continue;
        const full = join(dir, entry);
        const s = statSync(full);
        if (s.isDirectory()) out.push(...walk(full, exts));
        else if (exts.includes(extname(entry))) out.push(full);
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

// Match JSX attribute links in MDX: <LinkCard href="/path/" />. Starlight's
// card components take the destination as an attribute, so these never match
// `linkRe` — before this pattern existed they went entirely unvalidated.
const hrefRe = /\bhref=(?:"([^"\n]*)"|\{?'([^'\n]*)'\}?)/g;

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
        out.push({ target: m[2], line: offsets[m.index] ?? 1 });
    }
    for (const m of src.matchAll(hrefRe)) {
        // Exactly one of the two alternatives matches; the other is undefined.
        const target: string = m[1] || m[2] || "";
        if (target === "") continue;
        out.push({ target, line: offsets[m.index] ?? 1 });
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
        anchorsByFile.set(f, collectAnchors(stripFrontmatter(src), src));
    }
    return { anchorsByFile };
}

// Resolve a docs path (extensionless, with or without a trailing slash) to the
// backing content file. Starlight serves `foo/index.mdx` at `/foo/`, so index
// files are probed too — without that, every link to a section landing page
// like `/replacing-cron/` looks broken.
function isFile(abs: string): boolean {
    return existsSync(abs) && statSync(abs).isFile();
}

function findDocFile(relPath: string): string | undefined {
    const trimmed = relPath.replace(/\/+$/, "");
    const ext = extname(trimmed);
    if (ext !== "" && ext !== ".md" && ext !== ".mdx") {
        const abs = resolve(docsRoot, trimmed);
        return isFile(abs) ? abs : undefined;
    }
    const base = ext === "" ? trimmed : trimmed.slice(0, -ext.length);
    const candidates = [
        base + ".mdx",
        base + ".md",
        join(base, "index.mdx"),
        join(base, "index.md"),
    ];
    return candidates.map((c) => resolve(docsRoot, c)).find(isFile);
}

function resolveDocTarget(target: string, fromFile: string): string | undefined {
    const raw = stripHash(target);
    if (raw === "") return fromFile;
    const rel = raw.startsWith("/")
        ? raw.slice(1)
        : relative(docsRoot, resolve(dirname(fromFile), raw));
    return findDocFile(rel);
}

// Parse the `redirects` map out of astro.config.mjs. Only string-valued entries
// are used, which is the shape the config uses.
function loadRedirects(): Map<string, string> {
    const out = new Map<string, string>();
    if (!existsSync(astroConfigPath)) return out;
    const src = readFileSync(astroConfigPath, "utf8");
    const start = src.indexOf("redirects:");
    if (start < 0) return out;
    const open = src.indexOf("{", start);
    const close = src.indexOf("}", open);
    if (open < 0 || close < 0) return out;
    const block = src.slice(open + 1, close);
    for (const m of block.matchAll(/["']([^"']+)["']\s*:\s*["']([^"']+)["']/g)) {
        out.set(normalizePath(m[1]), m[2]);
    }
    return out;
}

function normalizePath(p: string): string {
    const trimmed = p.replace(/\/+$/, "");
    return trimmed === "" ? "/" : trimmed;
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

// A bare `#anchor` is a same-page link. It is *not* skipped — it is resolved
// against the containing file, so a heading that gets reworded breaks loudly.
function isAnchorOnly(target: string): boolean {
    return target.startsWith("#");
}

function isApiLink(path: string): boolean {
    return normalizePath(path) === "/api" || path.startsWith("/api/");
}

function stripHash(raw: string): string {
    const hash = raw.indexOf("#");
    return hash >= 0 ? raw.slice(0, hash) : raw;
}

// An asset is a reference to a real non-markdown file: an image, a video, a
// static JSON artifact. The test is the *extension*, not "does it resolve" —
// the previous behaviour treated every unresolvable extensionless path as an
// asset, which silently skipped every `/configuration/tasks/`-style link in the
// corpus (i.e. all of them) and made this script report success unconditionally.
function isAssetLink(target: string): boolean {
    if (isExternal(target) || isAnchorOnly(target)) return false;
    const raw = stripHash(target);
    if (raw === "") return false;
    if (generatedRoutes.has(normalizePath(raw))) return false;
    const ext = extname(raw.replace(/\/+$/, ""));
    return ext !== "" && ext !== ".md" && ext !== ".mdx";
}

function checkApiLink(pathPart: string, openapi: OpenApiIndex | undefined): string | undefined {
    if (!openapi) return "openapi.json not found; cannot validate /api/ link";
    const opId = pathPart.replace(/^\/api\/?/, "").replace(/\/$/, "");
    if (opId === "") return undefined;
    if (openapi.operationIds.has(opId) || openapi.operationIdSlugs.has(opId)) return undefined;
    return `operation '${opId}' not found in openapi.json`;
}

function checkAnchor(dest: string, fragment: string, index: DocIndex): string | undefined {
    if (!fragment) return undefined;
    const anchors = index.anchorsByFile.get(dest);
    if (anchors?.has(fragment) === true) return undefined;
    return `anchor '#${fragment}' not found in destination`;
}

function checkDocLink(
    pathPart: string,
    fragPart: string,
    fromFile: string,
    index: DocIndex,
    redirects: Map<string, string>,
): string | undefined {
    const dest = resolveDocTarget(pathPart, fromFile);
    if (dest) return checkAnchor(dest, fragPart, index);

    // Not a real page. If a redirect covers it the published URL still resolves,
    // but a file we control should name the live target instead.
    const redirectTarget = redirects.get(normalizePath(stripHash(pathPart)));
    if (redirectTarget !== undefined) {
        const suffix = fragPart ? ` (and '#${fragPart}' cannot survive the redirect)` : "";
        return `resolves only via a redirect to '${redirectTarget}' — link the live page directly${suffix}`;
    }
    return "destination file does not exist";
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
    redirects: Map<string, string>,
): Finding | undefined {
    const { target, line } = link;
    if (isExternal(target)) return undefined;
    if (isAssetLink(target)) return undefined;
    const reason = reasonFor(target, fromFile, index, openapi, redirects);
    if (reason === undefined) return undefined;
    return { file: relative(repoRoot, fromFile), line, target, reason };
}

// The one place a target is turned into a failure reason (or undefined for a
// good link). Shared by the docs-internal scan and the outbound URL scan so the
// two can never disagree about what counts as broken.
function reasonFor(
    target: string,
    fromFile: string,
    index: DocIndex,
    openapi: OpenApiIndex | undefined,
    redirects: Map<string, string>,
): string | undefined {
    const { path, fragment } = partitionTarget(target);
    if (generatedRoutes.has(normalizePath(path))) return undefined;
    if (isApiLink(path)) return checkApiLink(path, openapi);
    return checkDocLink(path, fragment, fromFile, index, redirects);
}

// Absolute docs URLs embedded in non-docs sources. Trailing punctuation that is
// clearly prose or code syntax (quote, backtick, paren, comma, period) is
// trimmed so `"https://docs.runwisp.com/operations/cli/".` yields a clean path.
// The path group is required, so a bare `https://docs.runwisp.com` with no path
// simply doesn't match — there is nothing to validate about the site root.
const outboundRe = new RegExp(String.raw`https://docs\.runwisp\.com(/[^\s"'\`)\]<>]*)`, "g");

// Expand the configured roots into the concrete files to scan.
function outboundFiles(): string[] {
    const out: string[] = [];
    for (const root of outboundRoots) {
        const abs = resolve(repoRoot, root.path);
        if (!existsSync(abs)) continue;
        out.push(...(statSync(abs).isDirectory() ? walk(abs, root.exts ?? [".md"]) : [abs]));
    }
    return out;
}

function checkOutboundFile(
    file: string,
    index: DocIndex,
    openapi: OpenApiIndex | undefined,
    redirects: Map<string, string>,
): { findings: Finding[]; checked: number } {
    const src = readFileSync(file, "utf8");
    if (!src.includes(SITE)) return { findings: [], checked: 0 };
    const offsets = buildLineOffsets(src);
    const findings: Finding[] = [];
    let checked = 0;
    for (const m of src.matchAll(outboundRe)) {
        const rawPath = m[1].replace(/[.,;:]+$/, "");
        checked++;
        if (normalizePath(rawPath) === "/") continue;
        if (isAssetLink(rawPath)) continue;
        // Outbound URLs are absolute, so they resolve from the docs root rather
        // than from the file that mentions them.
        const reason = reasonFor(rawPath, docsRoot, index, openapi, redirects);
        if (reason === undefined) continue;
        findings.push({
            file: relative(repoRoot, file),
            line: offsets[m.index] ?? 1,
            target: SITE + rawPath,
            reason,
        });
    }
    return { findings, checked };
}

function checkOutbound(
    index: DocIndex,
    openapi: OpenApiIndex | undefined,
    redirects: Map<string, string>,
): { findings: Finding[]; checked: number; files: number } {
    const findings: Finding[] = [];
    let checked = 0;
    let files = 0;
    for (const f of outboundFiles()) {
        const result = checkOutboundFile(f, index, openapi, redirects);
        if (result.checked === 0) continue;
        files++;
        checked += result.checked;
        findings.push(...result.findings);
    }
    return { findings, checked, files };
}

function checkDocsTree(
    files: string[],
    index: DocIndex,
    openapi: OpenApiIndex | undefined,
    redirects: Map<string, string>,
): { findings: Finding[]; checked: number } {
    const findings: Finding[] = [];
    let checked = 0;
    for (const f of files) {
        for (const link of extractLinks(readFileSync(f, "utf8"))) {
            if (isExternal(link.target) || isAssetLink(link.target)) continue;
            checked++;
            const finding = checkLink(link, f, index, openapi, redirects);
            if (finding !== undefined) findings.push(finding);
        }
    }
    return { findings, checked };
}

function report(findings: Finding[], summary: string): number {
    findings.sort((a, b) => (a.file === b.file ? a.line - b.line : a.file.localeCompare(b.file)));
    if (findings.length === 0) {
        console.log(`check-docs-links: ok — ${summary}`);
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
        `\ncheck-docs-links: ${String(findings.length)} broken link(s) across ${String(fileCount)} file(s) — ${summary}`,
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
    const redirects = loadRedirects();
    const internal = checkDocsTree(files, index, openapi, redirects);
    const findings: Finding[] = [...internal.findings];
    const internalChecked = internal.checked;
    const outbound = checkOutbound(index, openapi, redirects);
    findings.push(...outbound.findings);
    const summary =
        `validated ${String(internalChecked)} link(s) in ${String(files.length)} docs page(s) ` +
        `and ${String(outbound.checked)} outbound docs URL(s) in ${String(outbound.files)} repo file(s).`;
    return report(findings, summary);
}

process.exit(main());
