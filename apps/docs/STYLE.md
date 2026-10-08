# Docs style guide

How pages under `src/content/docs/` are written. Modeled on the
[Symfony documentation standards](https://symfony.com/doc/current/contributing/documentation/standards.html).
`bunx moon run docs:lint-prose` (part of `bun run ci`) enforces the rules marked
**(linted)**.

## Page types

- **Getting started**: a numbered path for a new user. Each page builds on the
  one before and ends where the next one starts. Show the smallest working
  example, then link to the page that owns the details.
- **Guides**: one page per feature. Open with one or two sentences on what it
  does, then the smallest working example. Order sections from common to rare.
  Edge cases and internals go last, or get cut if an operator never acts on them.
- **Reference**: every key and command, terse, lookup only.
  `configuration/tasks.mdx` is the exemplar: one heading per key, a
  `**type**`/`**default**` line, a one-line description, one example.

Merge into an existing page before adding a new one. A new page goes into the
sidebar in `astro.config.mjs` and into `SECTIONS` in `src/pages/llms.txt.ts`
(the build fails otherwise).

## One fact, one home

Each fact lives on one page; every other page links to its anchor and does not
restate it. Key semantics, types and defaults live in `configuration/*`. Other
homes:

- Reload vs restart: `operations/reload`
- Password, CHAP, `RUNWISP_AUTH`: `operations/auth`
- Failure model and run statuses: `configuration/tasks#failures`
- `import --write`, staging, `promote`: `coming-from/cron`
- Notifier secrets, routing, testing: `notifications/`
- CLI flags: `reference/cli`
- What the Docker image contains: `getting-started/docker`

## Language

- Second person ("you"), present tense, short sentences, plain words.
  Never "we" or "our" **(linted)**.
- No marketing, no rhetorical questions, no internal doctrine.
- Code first: lead a section with TOML or shell where you can. Explain why only
  when it changes what the operator does.
- No em dashes; use a comma, colon, or parentheses **(linted)**.
- Words to avoid **(linted)**: just, simply, easy, easily, obviously, basically,
  clearly, merely, of course, trivial, quickly. They tell the reader nothing,
  and "easy" makes a stuck reader feel worse.
- Headings are sentence case. Never rename a heading that something outside the
  docs links to (Go sources, README, `install.sh`); `docs:links` catches these.
- At most about two admonitions per page.

## Code blocks

- Every TOML block names its file **(linted)**: put `title="runwisp.toml"`
  after `toml` on the opening fence. Use the real file name when it is another
  file (`title="conf.d/backups.toml"`).
- A block that leaves out keys a working config needs marks the gap with
  `# ...`, so a fragment never looks complete:
    ```toml
    [tasks.report]
    # ...
    timeout = "10m"
    ```
- Use realistic names (`backup-db`, `worker`, `slack-ops`), never `foo`/`bar`.
  `hello` is fine: it is the task the starter config creates. Use
  `example.com` for domains.
- Shell blocks have no `$` prompt; they already render as a terminal.

## Version markers

Anything added after 1.0.0 says which release added it, so someone on an older
binary knows why it is rejected. In plain markdown, so the `.md` twin of the
page keeps it:

- On a reference key, append it to the type line:
  `**type**: \`bool\` **default**: \`false\` **since**: \`1.3\``
- On a page or a CLI command without a type line, put it on its own line right
  after the heading: `**since**: \`1.3\``
- For a new value of an existing key, say it in the sentence: "Since 1.3, ..."

## Images

- Every image has alt text. Start with a capital letter, end with a period.
- Don't start with "A screenshot of"; describe what the reader should see.
