// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Serves every docs page as raw markdown at `<slug>.md` (e.g.
// /configuration/tasks.md) *and* at `<slug>/index.md`, plus a one-line stub
// at every moved-page URL. The edge rewrite that hands AI agents Markdown
// instead of HTML (see cloudflare-rules.md) turns a trailing-slash request
// like `/configuration/tasks/` into `/configuration/tasks/index.md`, and a
// moved URL like `/operations/cli` into a 404 without the stub. The page
// body is the source markdown with frontmatter stripped, so agents can
// `curl` it without HTML noise. Linked from the curated /llms.txt index.

import type { APIRoute, GetStaticPaths, InferGetStaticPropsType } from "astro";
import { getCollection } from "astro:content";
import { redirects } from "../redirects.mjs";

// Leads each response with a UTF-8 BOM. Prerendered static files lose the
// Response charset header, and hosts (Cloudflare Pages) serve .md without one
// plus `nosniff`, so the BOM is what forces browsers to decode as UTF-8 — in
// `astro preview` and in production alike. Markdown/LLM consumers strip it.
const BOM = "﻿";
const SITE = "https://docs.runwisp.com";

type Props = { body: string };

export const getStaticPaths = (async () => {
    const docs = await getCollection("docs");
    const paths: { params: { slug: string }; props: Props }[] = [];

    for (const entry of docs) {
        const body = entry.body ?? "";
        paths.push({ params: { slug: entry.id }, props: { body } });
        // The root page's `<slug>.md` (index.md) already covers the `/`
        // twin; every other page also needs one at `<slug>/index.md`.
        if (entry.id !== "index") {
            paths.push({ params: { slug: `${entry.id}/index` }, props: { body } });
        }
    }

    for (const [source, target] of Object.entries(redirects)) {
        const body = `This page moved to ${new URL(target, SITE).href}.\n`;
        const slug = source.replace(/^\//, "");
        paths.push({ params: { slug }, props: { body } });
        paths.push({ params: { slug: `${slug}/index` }, props: { body } });
    }

    return paths;
}) satisfies GetStaticPaths;

type StaticProps = InferGetStaticPropsType<typeof getStaticPaths>;

export const GET: APIRoute<StaticProps> = ({ props }) => {
    return new Response(BOM + props.body, {
        headers: { "Content-Type": "text/markdown; charset=utf-8" },
    });
};
