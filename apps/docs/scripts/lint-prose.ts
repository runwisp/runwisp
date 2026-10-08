// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later
//
// lint-prose.ts: enforce the mechanical rules of apps/docs/STYLE.md on every
// page in src/content/docs. Judgment calls (one fact per home, page anatomy)
// stay with the reviewer; this only catches what a grep can.

import { readFileSync, readdirSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const docsRoot = resolve(dirname(fileURLToPath(import.meta.url)), "../src/content/docs");

// Filler that tells the reader nothing, plus "we" voice (STYLE.md: you, never we).
const BANNED =
    /\b(just|simply|easy|easily|obviously|basically|clearly|merely|of course|trivial|quickly|we|we'll|we're|our|ours)\b/i;

const problems: string[] = [];

const pages = readdirSync(docsRoot, { recursive: true, encoding: "utf8" }).filter((rel) =>
    rel.endsWith(".mdx"),
);

for (const rel of pages) {
    const lines = readFileSync(join(docsRoot, rel), "utf8").split("\n");
    let inFence = false;
    lines.forEach((line, i) => {
        const at = `src/content/docs/${rel}:${String(i + 1)}`;
        if (line.includes("—")) problems.push(`${at}: em dash, use a comma, colon, or parentheses`);

        const fence = /^\s*(```|~~~)(\S*)/.exec(line);
        if (fence) {
            if (!inFence && fence[2] === "toml" && !line.includes("title=")) {
                problems.push(
                    `${at}: toml block needs title="runwisp.toml" (or the real file name)`,
                );
            }
            inFence = !inFence;
            return;
        }
        if (inFence) return;

        // Inline code and link targets are not prose.
        const prose = line.replace(/`[^`]*`/g, "").replace(/\]\([^)]*\)/g, "]");
        const word = BANNED.exec(prose);
        if (word) problems.push(`${at}: "${word[1]}" (see STYLE.md, words to avoid)`);
    });
}

if (problems.length > 0) {
    console.error(problems.join("\n"));
    console.error(`\n${String(problems.length)} style problem(s). Rules: apps/docs/STYLE.md`);
    process.exit(1);
}
console.log("docs prose: ok");
