<!--
SPDX-FileCopyrightText: PoppyCake, s.r.o.
SPDX-License-Identifier: GPL-3.0-or-later
-->

# Cloudflare dashboard config

Not published to the site (outside `src/content/`) — this is operator setup, not docs content.

## Serve Markdown to AI agents

The docs site is a static Astro/Starlight build with no Worker, adapter, or `functions/`
directory. AI-agent content negotiation is done entirely at the edge with a Cloudflare
**Transform Rule** (Rules → Transform Rules → Rewrite URL), which runs before any Workers
invocation and stays on the **Free plan** (10 active rules, we use 1).

**Constraint:** the Free/Pro plans don't support regex in the Rules language (`matches`,
`regex_replace()` are Business/Enterprise only). So the rule can't strip a trailing slash or
pattern-match a User-Agent — it works around that by only matching paths that already end in
`/`, and by matching each bot User-Agent with a plain `contains`. The build side of this is
`src/pages/[...slug].md.ts`, which emits a `<slug>/index.md` twin for every page precisely so
`concat(path, "index.md")` is always correct.

**When incoming requests match…** (filter expression, plain field — no rewrite-field
restriction applies here):

```
(http.host eq "docs.runwisp.com")
and (http.request.method in {"GET" "HEAD"})
and (ends_with(http.request.uri.path, "/"))
and not (starts_with(http.request.uri.path, "/api/"))
and (
  http.request.headers["accept"][0] contains "text/markdown"
  or http.request.headers["accept"][0] contains "text/x-markdown"
  or lower(http.user_agent) contains "claude-user"
  or lower(http.user_agent) contains "claudebot"
  or lower(http.user_agent) contains "anthropic-ai"
  or lower(http.user_agent) contains "chatgpt-user"
  or lower(http.user_agent) contains "gptbot"
  or lower(http.user_agent) contains "oai-searchbot"
  or lower(http.user_agent) contains "perplexitybot"
  or lower(http.user_agent) contains "google-extended"
  or lower(http.user_agent) contains "cohere-ai"
  or lower(http.user_agent) contains "ccbot"
  or lower(http.user_agent) contains "meta-externalagent"
  or lower(http.user_agent) contains "bytespider"
  or lower(http.user_agent) contains "firecrawl"
  or lower(http.user_agent) contains "duckassistbot"
)
```

**Path → Rewrite to → Dynamic** (rewrite expression — restricted to `http.request.uri.*`,
`http.request.headers.*`, `http.request.accepted_languages`; `concat()` may appear once):

```
concat(http.request.uri.path, "index.md")
```

Notes:

- `/api/` is excluded: the starlight-openapi generated routes have no `.md` twin.
- Plain `contains` on `Accept` means `*/*` (curl, wget, uptime monitors) never matches and
  keeps getting HTML — that's the deliberate default (see `llms.txt.ts` header comment).
- If a future dashboard build reports `http.request.headers` as unavailable at this phase on
  the Free plan, fall back to the User-Agent clauses alone and rely on the HTML `<head>`
  comment (`src/components/Head.astro`) plus `/llms.txt` to reach everyone else. Do not reach
  for a Worker to route around it — see the git history of this file for why.

## Verifying after a dashboard change

```sh
D=https://docs.runwisp.com
curl -si -H 'Accept: text/markdown, text/html, */*' $D/configuration/tasks/ | head -20
curl -si -A 'Mozilla/5.0 (compatible; ChatGPT-User/1.0; +https://openai.com/bot)' $D/configuration/tasks/ | head -20
curl -si $D/configuration/tasks/ | head -20                            # curl -> HTML
curl -si -H 'Accept: text/html,*/*;q=0.8' $D/ | head -20               # browser -> HTML
curl -si -H 'Accept: text/markdown' $D/                                # root -> index.md
curl -si -H 'Accept: text/markdown' $D/recipes/migrating-from-cron/    # moved-to stub, not 404
curl -si -H 'Accept: text/markdown' $D/api/listTasks/ | head -5        # still HTML, not 404
curl -si $D/llms.txt | head -5                                         # untouched
```

Expect markdown responses to start with the page body (not `<!doctype html>`) and carry
`Content-Type: text/markdown; charset=utf-8`; HTML responses to carry the agent comment in
`<head>`.
