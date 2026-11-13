// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Turns a docs page's MDX source into plain Markdown for the `.md` export that
// agents read (llms.txt): imports are dropped, imported `.mdx` partials are
// inlined, `<Tabs>` become `#### Label` sections, and the remaining components
// become a line of text or nothing. Pure: the caller supplies how to read a
// partial, so the transform needs no filesystem.

export type PartialFile = { id: string; source: string };

export type MdxContext = {
    /** Absolute docs origin used to make site-relative links absolute. */
    site: string;
    /** Reads the file `specifier` names, relative to the file with id `from`. */
    resolve: (specifier: string, from: string) => PartialFile | undefined;
};

const MAX_DEPTH = 8;
// Starlight and local components that may appear without an import in older pages.
const KNOWN_COMPONENTS = new Set(["Tabs", "TabItem", "CardGrid", "LinkCard", "Card", "Screenshot"]);

const FENCE = /^\s*(`{3,}|~{3,})(.*)$/;
const COMPONENT_START = /^\s*<([A-Z][A-Za-z0-9]*)(?=[\s/>]|$)/;
const COMPONENT_END = /^\s*<\/([A-Z][A-Za-z0-9]*)>\s*$/;
const TAB_INDENT = 4;

export function mdxToMarkdown(source: string, fromId: string, ctx: MdxContext, depth = 0): string {
    const flattened = new Flattener(stripFrontmatter(source).split("\n"), fromId, ctx, depth).run();
    return flattened.replace(/\n{3,}/g, "\n\n").trim() + "\n";
}

/** One pass over a page's lines, holding what the pass has seen so far. */
class Flattener {
    private readonly out: string[] = [];
    private readonly imports = new Map<string, string>(); // local name -> specifier
    private fence = "";
    private dedent = 0;

    constructor(
        private readonly lines: string[],
        private readonly fromId: string,
        private readonly ctx: MdxContext,
        private readonly depth: number,
    ) {}

    run(): string {
        for (let i = 0; i < this.lines.length; i++) {
            i = this.consume(i);
        }
        return this.out.join("\n");
    }

    /** Handles the line (or multi-line statement/tag) at `i`; returns its last index. */
    private consume(i: number): number {
        const line = this.lines[i] ?? "";
        if (this.inFence(line)) return i;
        if (/^(import[\s{]|export\s)/.test(line)) return this.statement(i);
        // A bare `<div class="..." />` is a mount point for the page's script.
        if (/^\s*<div\s[^>]*\/>\s*$/.test(line)) return i;
        if (this.closingTag(line)) return i;

        const name = COMPONENT_START.exec(line)?.[1];
        if (name && this.isComponent(name)) return this.component(name, i);
        this.emit(imageToText(line));
        return i;
    }

    private emit(line: string): void {
        const lead = line.length - line.trimStart().length;
        this.out.push(line.slice(Math.min(lead, this.dedent)));
    }

    private isComponent(name: string): boolean {
        return KNOWN_COMPONENTS.has(name) || this.imports.has(name);
    }

    /** Keeps fenced code verbatim. True when `line` was part of a fence. */
    private inFence(line: string): boolean {
        const marker = FENCE.exec(line);
        if (this.fence) {
            this.emit(line);
            const closes = marker?.[2]?.trim() === "" && marker[1].startsWith(this.fence.charAt(0));
            if (closes && marker[1].length >= this.fence.length) this.fence = "";
            return true;
        }
        if (!marker) return false;
        this.fence = marker[1];
        this.emit(line);
        return true;
    }

    private statement(start: number): number {
        const end = statementEnd(this.lines, start);
        if ((this.lines[start] ?? "").startsWith("import")) {
            recordImports(this.lines.slice(start, end + 1).join(" "), this.imports);
        }
        return end;
    }

    private closingTag(line: string): boolean {
        const name = COMPONENT_END.exec(line)?.[1];
        if (!name || !this.isComponent(name)) return false;
        if (name === "TabItem") {
            this.dedent = 0;
            this.emit("");
        }
        return true;
    }

    private component(name: string, start: number): number {
        const end = tagEnd(this.lines, start);
        const tag = this.lines.slice(start, end + 1).join(" ");
        const first = this.lines[start] ?? "";
        const indent = first.length - first.trimStart().length;

        switch (name) {
            case "TabItem":
                this.emit("");
                this.emit(`#### ${attr(tag, "label") ?? ""}`.trimEnd());
                this.emit("");
                this.dedent = indent + TAB_INDENT;
                break;
            case "LinkCard":
                this.emit(linkCardLine(tag, this.ctx.site));
                break;
            case "Screenshot":
                this.emitScreenshot(attr(tag, "alt"));
                break;
            default:
                // Tabs, CardGrid and unknown wrappers are layout only.
                if (/\/>\s*$/.test(tag)) this.inlinePartial(name);
        }
        return end;
    }

    private emitScreenshot(alt: string | undefined): void {
        if (alt) this.emit(`_Screenshot: ${alt}_`);
    }

    private inlinePartial(name: string): void {
        const specifier = this.imports.get(name) ?? "";
        if (!specifier.endsWith(".mdx") || this.depth >= MAX_DEPTH) return;
        const partial = this.ctx.resolve(specifier, this.fromId);
        if (!partial) return;
        const body = mdxToMarkdown(partial.source, partial.id, this.ctx, this.depth + 1);
        for (const line of body.split("\n")) this.emit(line);
    }
}

function stripFrontmatter(source: string): string {
    return source.replace(/^---\r?\n[\s\S]*?\r?\n---\r?\n?/, "");
}

/** Last line of the import/export statement that starts at `start`. */
function statementEnd(lines: string[], start: number): number {
    for (let i = start; i < lines.length; i++) {
        const line = lines[i] ?? "";
        if (/["'];?\s*$/.test(line) || /^export\s.*[;}]\s*$/.test(line)) return i;
        if (i > start && line.trim() === "") return i - 1;
    }
    return start;
}

function recordImports(statement: string, into: Map<string, string>): void {
    const m = /^import\s+(.*?)\s+from\s+["']([^"']+)["']/.exec(statement);
    if (!m) return;
    const [, clause, specifier] = m;
    const braces = /\{([^}]*)\}/.exec(clause)?.[1] ?? "";
    for (const part of braces.split(",")) {
        const local = part.split(/\s+as\s+/).pop();
        if (local?.trim()) into.set(local.trim(), specifier);
    }
    const def = clause
        .replace(/\{[^}]*\}/, "")
        .replace(/,/g, "")
        .trim();
    if (def) into.set(def, specifier);
}

/** Last line of the tag that starts at `start` (attributes may span lines). */
function tagEnd(lines: string[], start: number): number {
    for (let i = start; i < lines.length; i++) {
        if (/>\s*$/.test(lines[i] ?? "")) return i;
    }
    return start;
}

function attr(tag: string, name: string): string | undefined {
    const m = new RegExp(`\\b${name}=(["'])(.*?)\\1`).exec(tag);
    return m?.[2];
}

function linkCardLine(tag: string, site: string): string {
    const href = attr(tag, "href") ?? "";
    const url = href.startsWith("/") ? new URL(href, site).href : href;
    const description = attr(tag, "description");
    return `- [${attr(tag, "title") ?? ""}](${url})${description ? `: ${description}` : ""}`;
}

// A relative image path points into src/assets, which the site serves under
// hashed names, so it can't be linked. Keep what the alt text says.
function imageToText(line: string): string {
    return line.replace(/!\[([^\]]*)\]\((?!https?:\/\/)[^)]*\)/g, (_all, alt: string) =>
        alt ? `_Screenshot: ${alt}_` : "",
    );
}
