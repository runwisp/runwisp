// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Serves every docs page as raw markdown at `<slug>.md` (e.g.
// /configuration/tasks.md). The body is the page's source markdown with
// frontmatter stripped, so agents can `curl` it without HTML noise.
// Linked from the curated /llms.txt index.

import type { APIRoute, GetStaticPaths, InferGetStaticPropsType } from "astro";
import { getCollection } from "astro:content";

// Leads each response with a UTF-8 BOM. Prerendered static files lose the
// Response charset header, and hosts (Cloudflare Pages) serve .md without one
// plus `nosniff`, so the BOM is what forces browsers to decode as UTF-8 — in
// `astro preview` and in production alike. Markdown/LLM consumers strip it.
const BOM = "\uFEFF";

export const getStaticPaths = (async () => {
    const docs = await getCollection("docs");
    return docs.map((entry) => ({
        params: { slug: entry.id },
        props: { entry },
    }));
}) satisfies GetStaticPaths;

type Props = InferGetStaticPropsType<typeof getStaticPaths>;

export const GET: APIRoute<Props> = ({ props }) => {
    const { entry } = props;
    return new Response(BOM + (entry.body ?? ""), {
        headers: { "Content-Type": "text/markdown; charset=utf-8" },
    });
};
