<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# RunWisp UI — Design Contract ("Runbook" aesthetic)

This is the single reference for restyling `@runwisp/ui` components. The look is an
**operator's terminal / runbook**: monospace chrome, hairline borders, small radii, flat
dark mode. It comes from the redesigned website. Every component must follow these rules so
the library reads as one system.

## Hard constraints (do not break these)

1. **Do NOT change component props / public APIs.** This is a restyle, not a rewrite.
   `apps/ui` has 40+ import sites; changing a prop name, variant enum, or slot breaks the
   build. Only touch markup classes, scoped `<style>`, and internal structure.
2. **No hardcoded colors.** Reference the semantic tokens (`bg-surface`, `text-on-surface`,
   `border-outline`, `text-primary`, `bg-primary`, the `-soft`/`-soft-text`/`-soft-border`
   sets) or the `--rw-*` custom properties. The palette lives in `theme-tokens.css` +
   `theme.css` — never inline a hex.
3. **Terminal/console surfaces stay dark in BOTH themes.** `LogConsole`, code blocks, and
   any "device" surface use the fixed `--rw-con-*` / `--rw-term-*` / `--rw-syn-*` palettes
   and must NOT flip with light/dark.

## Typography

- **Body / sans:** `font-sans` = TASA Orbiter. Used only for prose/body copy.
- **Headings + all chrome:** `font-mono` = Geist Mono (variable, 100–900). Headings, labels,
  nav, buttons, badges, table headers, code — all mono.
- **Headings (`h1`–`h3` and `Heading`):** `font-mono`, weight **800**, line-height 1.1,
  letter-spacing `-0.02em`, `text-wrap: balance`. (Base rule is set globally in `theme.css`;
  components should not fight it.)
- **Eyebrow / kicker:** mono, ~11.5px, weight 500, letter-spacing `0.16em`, `uppercase`,
  color = `text-info` / teal-bright (`--rw-info` bright variant). This is the tab/label voice.
- **Brand lockup** (the mark + `RunWisp` wordmark): the one deliberate exception — wordmark is
  `font-sans` weight 700, `-0.02em`, matching the website nav. Brand voice, not chrome.

### Mono vs sans — the deciding rule

The two fonts split by _what the text is_, never by where it sits. The same value must never
render mono in one panel and sans in another (a task name mono in the sidebar but sans in a
card is the exact bug this rule exists to prevent).

**Mono** — anything the operator could type, grep, or paste. It is a token, not a sentence:

- task names, run IDs, ULIDs, fingerprints, hostnames, PIDs
- cron expressions, durations (`15s`, `167ms`), timestamps, exit codes
- file paths, shell commands, env var names, config keys, TOML values, URLs
- counts and numeric stats (`14/14`, `100%`), version strings
- all chrome: nav items, buttons, tabs, badges, table headers, section headings, eyebrows,
  status labels, keyboard hints

**Sans** — anything written for a human to read as prose:

- descriptions, empty-state sentences ("Nothing waiting for triage")
- relative time phrases ("2 minutes ago", "in 4 minutes")
- helper/hint text under form fields, error messages, tooltips, docs copy

When a line mixes both, mark up only the token: `<span class="font-mono">healthcheck-api</span>
failed 3 times`. Do not set the whole line mono to avoid a nested span.

## Radii (small — this is signature)

- **Controls** (buttons, inputs, tabs, chips, small pills): **3px** (`rounded-[3px]`).
- **Panels / cards / code boxes / popovers / modals**: **4px** (`rounded-[4px]`).
- **macOS-window shell** (if used): 11px.
- **Round pills / avatars / status dots / toggles**: `rounded-full`.
- Do not use the old `rounded-lg`/`rounded-xl` (8–12px) look anywhere.

## Borders & elevation

- **Everything is bordered.** Hairline `border border-outline`. Structure comes from lines,
  not fills.
- **Dark mode = borders only, NO shadow.** Do not apply `shadow-*` when dark. If a component
  needs elevation, gate the shadow to light only (e.g. a `dark:shadow-none` or a token that
  resolves to `none` in dark).
- **Light mode = restrained elevation.** Panels/cards: `0 1px 2px rgba(16,41,43,.04),
0 12px 28px -20px rgba(16,41,43,.22)`. Terminal-surfaced things get a slightly deeper drop.
  Prefer the existing `--shadow-sm/md` tokens (retuned in Phase 1) over inline shadows.

## Interaction

- **Focus ring:** `2px solid` teal-bright (`--rw-ring`), offset 2px. Keep it crisp — no big
  soft glow rings.
- **Hover:** lift toward teal — border goes `border-outline-hover`, text/icon can go
  `text-primary`; ghost surfaces get `bg-surface-sunken`.
- **Active (buttons):** `active:translate-y-[1px]` (the physical key-press feel). Keep the
  existing `active:` color darkening.

### Motion — instant by default

A transition is a cost paid on every interaction, so it has to buy something. **Hover, focus,
active and state changes are instant.** Do not add `transition-colors`, `transition-all`,
`transition-shadow` or `transition-opacity` to anything; a hover that fades in over 150ms is
a hover that feels 150ms late. Menus, popovers, dropdowns and inline error messages appear
instantly too — no scale-in, no fade-in.

Motion is only correct when the movement itself is the information:

- **Something is working** — spinners, skeleton pulse, the live/running dot pulse.
- **Something moved in space** — a drawer or sheet sliding from an edge, so you know where it
  came from and where it went.
- **A value changed continuously** — a progress bar's width, an accordion's height.
- **A full-viewport surface appeared** — a backdrop fade, so the page doesn't flash.

Everything else is decoration. When motion is warranted, keep it ≤150ms and set the timing
inline — there is no duration scale to reach for.

- Respect `prefers-reduced-motion` — no essential motion.

## Buttons (`button-styles.ts`)

- Mono, ~13.5px, weight 500, padding ≈ `0.72em 1.15em`, radius **3px**.
- `primary`: teal fill (`bg-primary text-on-primary`), hover `bg-primary-hover`,
  `active:translate-y-[1px]` + `active:bg-primary-active`.
- `secondary` / `ghost`: transparent or raised surface, **border** `border-outline`, hover
  `border-outline-hover` + `text-primary`.
- `danger` / `success`: matching semantic fill, same shape.
- Sizes keep the existing `xs|sm|md|lg` enum — only adjust the class strings, not the names.

## Badges / pills / chips

- Mono, tracked, small. The default "badge" reads like a config key / `.kw` token: a soft-teal
  pill (`bg-primary-soft text-primary-soft-text`) with a hairline `border-primary-soft-border`,
  3px radius. Status variants use the matching `-soft` token trio.

## Terminal / code surfaces

- Background `--rw-con-bg` / `--rw-term` (fixed dark). Text `--rw-con-text`. Dim/gutter
  `--rw-con-dim` / `--rw-con-gutter`.
- Syntax tokens: comment `--rw-syn-comment`, keyword `--rw-syn-kw`, string `--rw-syn-str`,
  number `--rw-syn-num`, punctuation `--rw-syn-punc`, function `--rw-syn-fn`.
- These are added to `theme-tokens.css` in Phase 1. Use them; do not invent new ones.

## Layout voice

- Section labels sit on a hairline as an uppercase mono "tab" (the eyebrow style above).
- **Pane tab / pane tag** (the website's `.pane-tab` / `.pane-tag`): a panel's title can ride
  its own top border — absolutely positioned, `-translate-y-1/2`, `bg-surface-sunken px-2` to
  knock out the line, mono ~10.5px, **lowercase**, tracked `0.06em`. A short state token may
  ride the same line on the right, colored by the `-soft-text` tokens. Lowercase is what
  separates a pane title from an eyebrow; don't mix the two voices on one element.
- **Tint is for alarms only.** In a row of stat panes, every pane sits on plain
  `border-outline bg-surface-raised` and carries its state in the tag word. Only a genuine
  problem (something to triage) gets a tinted surface, so a failure is the one lit thing on
  the page. Tinting the healthy states too is what turns a status row into decoration.
- **No framed icon badges.** A big mono number in a tab-titled pane says "operator console";
  a soft-tinted rounded square holding a lucide glyph says "dashboard template". If a stat
  needs an icon to be legible, the label is wrong.
- ASCII affordances are welcome where they already fit the component (`❯`, `$`, `──`,
  `[copy]` bracket buttons) but don't force them into generic primitives.

## When in doubt

Match `/Users/riki137/boxes/website/src/styles/app.css` (the `.btn`, `.pane`, `.nav`,
`.eyebrow`, `.codebox` classes) — it is the canonical visual. But express it through the
semantic tokens above, never by copying its raw hex values into a component.
