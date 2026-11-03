// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later
//
// test-mdx-to-markdown.ts: checks the MDX-to-Markdown transform behind the `.md`
// export, on small inputs and on every real page (no JSX, no imports, no
// relative image paths may survive). Run with `bun scripts/test-mdx-to-markdown.ts`.

import assert from "node:assert/strict";
import { readFileSync, readdirSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { type MdxContext, mdxToMarkdown } from "../src/lib/mdx-to-markdown";

const SITE = "https://docs.runwisp.com";
const docsRoot = resolve(dirname(fileURLToPath(import.meta.url)), "../src/content/docs");

const files = new Map<string, string>([
    ["/p/shared/_inner.mdx", "---\ntitle: x\n---\nInner text.\n"],
    [
        "/p/shared/_outer.mdx",
        'import Inner from "./_inner.mdx";\n\n**type**: `string`\n\n<Inner />\n',
    ],
]);
const memory: MdxContext = {
    site: SITE,
    resolve: (specifier, from) => {
        const id = resolve(dirname(from), specifier);
        const source = files.get(id);
        return source === undefined ? undefined : { id, source };
    },
};

const page = [
    'import { Tabs, TabItem } from "@astrojs/starlight/components";',
    'import Outer from "./shared/_outer.mdx";',
    'import Screenshot from "../../../components/Screenshot.astro";',
    "",
    "## Key",
    "",
    "<Outer />",
    "",
    '<Tabs syncKey="k">',
    '    <TabItem label="Script" icon="seti:shell">',
    "        Run it:",
    "",
    "        ```bash",
    '        import x from "kept";',
    "        curl https://get.runwisp.com | sh",
    "        ```",
    "    </TabItem>",
    '    <TabItem label="Brew">',
    "        ![Alt text](../../../assets/shot.png)",
    "    </TabItem>",
    "</Tabs>",
    "",
    "<Screenshot",
    '    name="web-ui"',
    '    alt="Web UI dashboard."',
    "/>",
    "",
    "<CardGrid>",
    '    <LinkCard title="From cron" description="Take over." href="/coming-from/cron/" />',
    "</CardGrid>",
    "",
    "Use <TOKEN> in prose.",
    "",
].join("\n");

const got = mdxToMarkdown(page, "/p/page.mdx", memory);
assert.equal(
    got,
    [
        "## Key",
        "",
        "**type**: `string`",
        "",
        "Inner text.",
        "",
        "#### Script",
        "",
        "Run it:",
        "",
        "```bash",
        'import x from "kept";',
        "curl https://get.runwisp.com | sh",
        "```",
        "",
        "#### Brew",
        "",
        "_Screenshot: Alt text_",
        "",
        "_Screenshot: Web UI dashboard._",
        "",
        `- [From cron](${SITE}/coming-from/cron/): Take over.`,
        "",
        "Use <TOKEN> in prose.",
        "",
    ].join("\n"),
);

// Every real page flattens cleanly, with partials resolved from disk.
const disk: MdxContext = {
    site: SITE,
    resolve: (specifier, from) => {
        const id = resolve(dirname(from), specifier);
        return { id, source: readFileSync(id, "utf8") };
    },
};
const pages = readdirSync(docsRoot, { recursive: true, encoding: "utf8" }).filter(
    (rel) => rel.endsWith(".mdx") && !rel.split("/").includes("shared"),
);
for (const rel of pages) {
    const file = join(docsRoot, rel);
    const md = mdxToMarkdown(readFileSync(file, "utf8"), file, disk);
    // Prose outside code fences only: `import` is legitimate inside examples.
    const prose = md.replace(/^(\s*)(```|~~~)[\s\S]*?^\1\2\s*$/gm, "");
    assert.doesNotMatch(prose, /^import\s/m, `${rel}: import line left`);
    assert.doesNotMatch(
        prose,
        /<\/?(Tabs|TabItem|CardGrid|LinkCard|Screenshot)\b/,
        `${rel}: JSX left`,
    );
    assert.doesNotMatch(md, /\]\(\.\.?\//, `${rel}: relative path left`);
    assert.doesNotMatch(prose, /^\s*<[A-Z][A-Za-z]*\s*\/>\s*$/m, `${rel}: component left`);
}
assert.ok(pages.length > 20, "expected to check the real docs pages");

console.log(`mdx-to-markdown: ok (${String(pages.length)} pages)`);
