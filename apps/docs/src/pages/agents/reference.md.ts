// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Serves the hand-written dense agent reference at /agents/reference.md.
// The source lives outside public/ so Prettier manages it (a BOM in a static
// .md source would be stripped by Prettier); the BOM is added here at request
// time, matching the per-page `.md` endpoint. Linked from /llms.txt.

import type { APIRoute } from "astro";
import reference from "../../agents/reference.md?raw";

const BOM = "\uFEFF";

export const GET: APIRoute = () =>
    new Response(BOM + reference, {
        headers: { "Content-Type": "text/markdown; charset=utf-8" },
    });
