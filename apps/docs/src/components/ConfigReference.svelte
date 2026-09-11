<!--
  SPDX-FileCopyrightText: PoppyCake, s.r.o.
  SPDX-License-Identifier: GPL-3.0-or-later

  Interactive runwisp.toml reference. Key STRUCTURE (types, defaults, enums,
  ranges) is walked out of the daemon's source-of-truth JSON Schema
  (apps/runwisp/internal/config/config.schema.json), so it can never drift.
  Friendly per-key copy lives in config-copy.ts; cross-cutting prose and the
  cluster layout live in config-topics.ts — a Topic renders as a first-class
  row alongside the keys, never inside a key's body.

  Iteration 1 renders only the [tasks.<name>] table; add more buildSection(...)
  surfaces and clusters to grow it.
-->
<script lang="ts">
    import { onMount } from "svelte";
    import { SvelteSet } from "svelte/reactivity";
    import { slide } from "svelte/transition";
    import schemaDoc from "../../../runwisp/internal/config/config.schema.json";
    import { configCopy, fieldOrder, type ValueExample } from "./config-copy";
    import { clusters, type Cluster, type Prose, type Topic } from "./config-topics";
    // Cropped Web UI screenshots for the couple of keys a picture explains faster
    // than prose. Astro hands these to the island as ImageMetadata (not a bare
    // URL), so read .src when rendering (see imgSrc + SHOTS below).
    import type { ImageMetadata } from "astro";
    import groupLight from "../assets/screenshots/group-sidebar-light.png";
    import groupDark from "../assets/screenshots/group-sidebar-dark.png";
    import paramsLight from "../assets/screenshots/params-modal-light.png";
    import paramsDark from "../assets/screenshots/params-modal-dark.png";

    type Json = string | number | boolean | null | Json[] | { [k: string]: Json };

    interface SchemaNode {
        $ref?: string;
        type?: string | string[];
        description?: string;
        default?: Json;
        enum?: string[];
        minimum?: number;
        maximum?: number;
        pattern?: string;
        properties?: Record<string, SchemaNode>;
        additionalProperties?: SchemaNode | boolean;
        items?: SchemaNode;
        required?: string[];
    }

    interface SchemaDoc {
        $defs: Record<string, SchemaNode>;
        properties: Record<string, SchemaNode>;
    }

    interface Field {
        id: string;
        name: string;
        typeLabel: string;
        required: boolean;
        description?: string;
        hint?: string;
        example?: string;
        default?: string;
        enumValues?: string[];
        itemEnum?: string[];
        values?: ValueExample[];
        min?: number;
        max?: number;
        nested?: Field[];
        nestedLabel?: string;
    }

    interface Section {
        id: string;
        header: string;
        description?: string;
        fields: Field[];
    }

    const schema: SchemaDoc = schemaDoc;

    // Generic format samples for duration/size fields that don't carry a curated
    // list — friendlier than showing the validation regex.
    const DURATION_SAMPLES: ValueExample[] = [
        { v: "500ms", hint: "half a second" },
        { v: "30s", hint: "30 seconds" },
        { v: "5m", hint: "5 minutes" },
        { v: "1h", hint: "1 hour" },
        { v: "1h30m", hint: "90 minutes (mixed units)" },
    ];
    const SIZE_SAMPLES: ValueExample[] = [
        { v: "512kb", hint: "512 kilobytes" },
        { v: "100mb", hint: "100 megabytes" },
        { v: "2gb", hint: "2 gigabytes" },
        { v: "0", hint: "no cap" },
    ];

    // Keys that show a cropped Web UI screenshot (light/dark swap on Starlight's
    // html[data-theme]). Astro imports resolve to ImageMetadata or a URL string
    // depending on config, so normalise to a URL with imgSrc.
    interface Shot {
        light: ImageMetadata | string;
        dark: ImageMetadata | string;
        alt: string;
        caption: string;
    }
    const SHOTS: Record<string, Shot> = {
        "tasks.group": {
            light: groupLight,
            dark: groupDark,
            alt: "Web UI sidebar with tasks filed under BACKUPS and HEALTH group headers",
            caption: "Each group becomes a section header in the sidebar.",
        },
        "tasks.params": {
            light: paramsLight,
            dark: paramsDark,
            alt: "Web UI Run Task dialog with a form field per declared parameter",
            caption: "Declared params become a form on the Run Task dialog.",
        },
    };
    function imgSrc(img: ImageMetadata | string): string {
        return typeof img === "string" ? img : img.src;
    }

    function resolve(node: SchemaNode): { node: SchemaNode; ref?: string } {
        if (node.$ref) {
            const ref = node.$ref.replace("#/$defs/", "");
            return { node: schema.$defs[ref], ref };
        }
        return { node };
    }

    function fmtValue(v: Json): string {
        if (Array.isArray(v)) return "[" + v.map(fmtValue).join(", ") + "]";
        if (typeof v === "string") return `"${v}"`;
        return String(v);
    }

    function primitive(t: string | string[] | undefined): string {
        if (Array.isArray(t)) return t.join(" | ");
        return t ?? "string";
    }

    function typeLabel(node: SchemaNode, ref: string | undefined): string {
        if (node.enum) return "enum";
        switch (ref) {
            case "duration":
                return "duration";
            case "byteSize":
                return "size";
            case "failures":
                return "failures";
            case "envMap":
            case "secretsMap":
                return "map";
        }
        if (node.type === "array" && node.items) {
            const item = resolve(node.items);
            if (item.node.enum) return "enum[]";
            if (item.node.properties) return "table";
            return `${primitive(item.node.type)}[]`;
        }
        if (node.type === "object") return "map";
        return primitive(node.type);
    }

    function buildField(
        name: string,
        raw: SchemaNode,
        requiredList: string[],
        idPrefix: string,
    ): Field {
        const { node, ref } = resolve(raw);
        const description = raw.description ?? node.description;
        const field: Field = {
            id: `${idPrefix}.${name}`,
            name,
            required: requiredList.includes(name),
            description,
            typeLabel: typeLabel(node, ref),
        };

        // When a property overrode the $def's own description, keep the $def
        // description as a secondary hint (e.g. the duration/size grammar).
        if (ref && raw.description && node.description && node.description !== description) {
            field.hint = node.description;
        }

        const def = raw.default ?? node.default;
        if (def !== undefined) field.default = fmtValue(def);

        const enumVals = node.enum ?? raw.enum;
        if (enumVals) field.enumValues = enumVals;
        if (node.minimum !== undefined) field.min = node.minimum;
        if (node.maximum !== undefined) field.max = node.maximum;

        attachArrayInfo(field, node, `[${idPrefix}.<name>.${name}]`);
        applyCopy(field);
        return field;
    }

    // For an array-typed field, either surface its item enum (e.g. route kinds)
    // or expand an object item into a nested field table (e.g. tasks.params).
    function attachArrayInfo(field: Field, node: SchemaNode, nestedLabel: string): void {
        if (node.type !== "array" || !node.items) return;
        const item = resolve(node.items);
        if (item.node.enum) {
            field.itemEnum = item.node.enum;
        } else if (item.node.properties) {
            const req = item.node.required ?? [];
            field.nested = Object.entries(item.node.properties).map(([n, p]) =>
                buildField(n, p, req, field.id),
            );
            field.nestedLabel = nestedLabel;
        }
    }

    // Curated, human-friendly copy wins over the terse schema description and
    // carries a worked example. Keys without an entry keep the schema text.
    function applyCopy(field: Field): void {
        const copy = configCopy[field.id];
        if (copy) {
            field.description = copy.summary;
            field.hint = undefined;
            if (copy.example) field.example = copy.example;
            if (copy.values) field.values = copy.values;
        }
        // Enums already list their values as chips; otherwise show format samples
        // for duration/size fields so nobody has to read a regex.
        if (!field.values && !field.enumValues) {
            if (field.typeLabel === "duration") field.values = DURATION_SAMPLES;
            else if (field.typeLabel === "size") field.values = SIZE_SAMPLES;
        }
    }

    // Most-common-first display order (see fieldOrder); unlisted keys keep schema
    // order after the listed ones. Array.sort is stable, so ties are preserved.
    function orderFields(id: string, fields: Field[]): Field[] {
        const order = fieldOrder[id];
        if (!order) return fields;
        const rank = (name: string): number => {
            const i = order.indexOf(name);
            return i === -1 ? order.length : i;
        };
        return [...fields].sort((a, b) => rank(a.name) - rank(b.name));
    }

    function buildSection(id: string, header: string, defName: string): Section {
        const def = schema.$defs[defName];
        const req = def.required ?? [];
        const fields = Object.entries(def.properties ?? {}).map(([n, p]) =>
            buildField(n, p, req, id),
        );
        return { id, header, description: def.description, fields: orderFields(id, fields) };
    }

    // Iteration 1: [tasks.*] only. Append more buildSection(...) calls to grow.
    const taskSection: Section = buildSection("tasks", "[tasks.<name>]", "task");
    const fieldByName = new Map<string, Field>(taskSection.fields.map((f) => [f.name, f]));

    // A block in a cluster is either a schema key or an authored topic. Both
    // render as collapsible, searchable, deep-linkable rows — topics never live
    // inside a key's body.
    type Block = { kind: "key"; field: Field } | { kind: "topic"; topic: Topic };
    interface RenderCluster {
        id: string;
        title: string;
        lead?: Topic;
        blocks: Block[];
    }

    const topicElemId = (t: Topic): string => `tasks.${t.id}`;

    // Resolve the authored clusters against the schema-built fields. Layout is
    // driven here; types/defaults/enums still come straight from the schema.
    function resolveClusters(list: Cluster[]): RenderCluster[] {
        const used = new SvelteSet<string>();
        const out: RenderCluster[] = list.map((c) => {
            let lead: Topic | undefined;
            const blocks: Block[] = [];
            for (const entry of c.entries) {
                if (typeof entry === "string") {
                    const field = fieldByName.get(entry);
                    if (field) {
                        blocks.push({ kind: "key", field });
                        used.add(entry);
                    }
                } else if (entry.lead) {
                    lead = entry;
                } else {
                    blocks.push({ kind: "topic", topic: entry });
                }
            }
            return { id: c.id, title: c.title, lead, blocks };
        });
        // Safety net: a schema key not placed in any cluster still shows, so a new
        // key can never silently vanish from the reference.
        const leftover = taskSection.fields.filter((f) => !used.has(f.name));
        if (leftover.length > 0) {
            out.push({
                id: "other",
                title: "Other",
                blocks: leftover.map((field) => ({ kind: "key", field })),
            });
        }
        return out;
    }

    const renderClusters = resolveClusters(clusters);

    let query = $state("");
    const openIds = new SvelteSet<string>();

    const q = $derived(query.trim().toLowerCase());

    function fieldMatches(f: Field): boolean {
        if (q === "") return true;
        if (f.name.toLowerCase().includes(q)) return true;
        if (f.description && f.description.toLowerCase().includes(q)) return true;
        if (f.enumValues && f.enumValues.some((v) => v.toLowerCase().includes(q))) return true;
        if (f.nested && f.nested.some(fieldMatches)) return true;
        return false;
    }

    function proseText(node: Prose): string {
        return "p" in node ? node.p : node.ul.join(" ");
    }

    function topicMatches(t: Topic): boolean {
        if (q === "") return true;
        if (t.title.toLowerCase().includes(q)) return true;
        if (t.summary.toLowerCase().includes(q)) return true;
        if (t.body?.some((n) => proseText(n).toLowerCase().includes(q))) return true;
        return false;
    }

    const filteredClusters = $derived(
        renderClusters
            .map((c) => ({
                ...c,
                lead: c.lead && (q === "" || topicMatches(c.lead)) ? c.lead : undefined,
                blocks: c.blocks.filter((b) =>
                    b.kind === "key" ? fieldMatches(b.field) : topicMatches(b.topic),
                ),
            }))
            .filter((c) => c.blocks.length > 0 || c.lead),
    );
    // The count tracks keys, not topics — "42 keys" stays a truthful key tally.
    const matchCount = $derived(
        filteredClusters.reduce((n, c) => n + c.blocks.filter((b) => b.kind === "key").length, 0),
    );

    function isOpen(id: string): boolean {
        return q !== "" || openIds.has(id);
    }

    function isExpanded(f: Field): boolean {
        return isOpen(f.id);
    }

    function toggle(id: string): void {
        if (openIds.has(id)) openIds.delete(id);
        else openIds.add(id);
    }

    function expandAll(): void {
        for (const c of renderClusters)
            for (const b of c.blocks)
                openIds.add(b.kind === "key" ? b.field.id : topicElemId(b.topic));
    }

    function collapseAll(): void {
        openIds.clear();
    }

    interface Segment {
        text: string;
        code?: boolean;
        href?: string;
    }

    // Split copy into plain text, inline-code (backtick pairs), and links
    // (`[label](#tasks.other_key)`). Content is trusted (authored in
    // config-copy.ts), so no full markdown parser is needed.
    function pushText(out: Segment[], raw: string): void {
        raw.split("`").forEach((part, i) => {
            if (part !== "") out.push({ text: part, code: i % 2 === 1 });
        });
    }
    function inlineSegments(text: string): Segment[] {
        const out: Segment[] = [];
        let last = 0;
        for (const m of text.matchAll(/\[([^\]]+)\]\((#[^)]+)\)/g)) {
            const idx = m.index ?? 0;
            if (idx > last) pushText(out, text.slice(last, idx));
            out.push({ text: m[1], href: m[2] });
            last = idx + m[0].length;
        }
        if (last < text.length) pushText(out, text.slice(last));
        return out;
    }

    // The collapsed one-liner has no room for markup: drop code ticks and turn a
    // link into its bare label.
    function plainText(text: string): string {
        return text.replace(/\[([^\]]+)\]\((#[^)]+)\)/g, "$1").replaceAll("`", "");
    }

    function highlight(text: string): { text: string; hit: boolean }[] {
        if (q === "") return [{ text, hit: false }];
        const idx = text.toLowerCase().indexOf(q);
        if (idx === -1) return [{ text, hit: false }];
        return [
            { text: text.slice(0, idx), hit: false },
            { text: text.slice(idx, idx + q.length), hit: true },
            { text: text.slice(idx + q.length), hit: false },
        ];
    }

    // Open (and scroll to) the field a hash points at — on first load and every
    // time an in-page `[label](#tasks.key)` link changes the hash.
    function openHash(): void {
        const rawHash = location.hash.replace(/^#/, "");
        if (rawHash === "") return;
        const hash = decodeURIComponent(rawHash);
        const parts = hash.split(".");
        for (let i = 2; i <= parts.length; i++) openIds.add(parts.slice(0, i).join("."));
        requestAnimationFrame(() => {
            document.getElementById(hash)?.scrollIntoView({ block: "center" });
        });
    }

    onMount(() => {
        openHash();
        window.addEventListener("hashchange", openHash);
        return () => window.removeEventListener("hashchange", openHash);
    });
</script>

<div class="rw-cfg">
    <div class="rw-cfg-toolbar">
        <input
            class="rw-cfg-search"
            type="search"
            placeholder="Search keys, values, descriptions…"
            bind:value={query}
            aria-label="Search configuration keys"
        />
        <span class="rw-cfg-count">{matchCount} {matchCount === 1 ? "key" : "keys"}</span>
        <div class="rw-cfg-actions">
            <button type="button" onclick={expandAll} disabled={q !== ""}>Expand all</button>
            <button type="button" onclick={collapseAll} disabled={q !== ""}>Collapse all</button>
        </div>
    </div>

    <section class="rw-cfg-section" id="tasks">
        <h2 class="rw-cfg-section-header"><code>{taskSection.header}</code></h2>
        {#if taskSection.description}
            <p class="rw-cfg-section-desc">{taskSection.description}</p>
        {/if}
        {#if filteredClusters.length === 0}
            <p class="rw-cfg-empty">Nothing matches “{query}”.</p>
        {/if}
        {#each filteredClusters as cluster (cluster.id)}
            <div class="rw-cfg-cluster" id={cluster.id}>
                <h3 class="rw-cfg-cluster-title">{cluster.title}</h3>
                {#if cluster.lead}{@render leadBlock(cluster.lead)}{/if}
                <div class="rw-cfg-fields">
                    {#each cluster.blocks as block (block.kind === "key" ? block.field.id : block.topic.id)}
                        {#if block.kind === "key"}
                            {@render fieldRow(block.field)}
                        {:else}
                            {@render topicRow(block.topic)}
                        {/if}
                    {/each}
                </div>
            </div>
        {/each}
    </section>
</div>

{#snippet fieldRow(field: Field)}
    <div class="rw-cfg-field" id={field.id} class:open={isExpanded(field)}>
        <button
            type="button"
            class="rw-cfg-field-head"
            aria-expanded={isExpanded(field)}
            onclick={() => toggle(field.id)}
        >
            <span class="rw-cfg-chevron" aria-hidden="true">▸</span>
            <code class="rw-cfg-key"
                >{#each highlight(field.name) as seg, i (i)}{#if seg.hit}<mark>{seg.text}</mark
                        >{:else}{seg.text}{/if}{/each}</code
            >
            <span class="rw-cfg-type">{field.typeLabel}</span>
            {#if field.required}<span class="rw-cfg-badge">required</span>{/if}
            {#if field.default}<span class="rw-cfg-default">= {field.default}</span>{/if}
            {#if field.description}
                <span class="rw-cfg-inline-desc">{plainText(field.description)}</span>
            {/if}
        </button>

        {#if isExpanded(field)}
            <div class="rw-cfg-body" transition:slide={{ duration: 150 }}>
                {#if field.description}
                    <p class="rw-cfg-desc">
                        {#each inlineSegments(field.description) as seg, i (i)}{#if seg.href}<a
                                    class="rw-cfg-link"
                                    href={seg.href}>{seg.text}</a
                                >{:else if seg.code}<code class="rw-cfg-inline-code"
                                    >{seg.text}</code
                                >{:else}{seg.text}{/if}{/each}
                    </p>
                {/if}
                {#if field.hint}<p class="rw-cfg-hint">{field.hint}</p>{/if}

                {#if SHOTS[field.id]}
                    {@const shot = SHOTS[field.id]}
                    <figure class="rw-cfg-shot">
                        <img
                            class="rw-cfg-shot-img rw-cfg-shot-light"
                            src={imgSrc(shot.light)}
                            alt={shot.alt}
                            loading="lazy"
                        />
                        <img
                            class="rw-cfg-shot-img rw-cfg-shot-dark"
                            src={imgSrc(shot.dark)}
                            alt=""
                            loading="lazy"
                        />
                        <figcaption>{shot.caption}</figcaption>
                    </figure>
                {/if}

                {#if field.example}
                    <pre class="rw-cfg-example"><code>{field.example}</code></pre>
                {/if}

                {#if field.values}
                    <div class="rw-cfg-samples">
                        <span class="rw-cfg-values-label"
                            >{field.enumValues ? "Possible values" : "Examples"}</span
                        >
                        <ul class="rw-cfg-sample-list">
                            {#each field.values as s (s.v)}
                                <li>
                                    <code class="rw-cfg-chip">{s.v}</code>
                                    <span class="rw-cfg-sample-hint">{s.hint}</span>
                                </li>
                            {/each}
                        </ul>
                    </div>
                {/if}

                {#if field.enumValues && !field.values}
                    <div class="rw-cfg-values">
                        <span class="rw-cfg-values-label">Possible values</span>
                        <span class="rw-cfg-chips">
                            {#each field.enumValues as v (v)}<code class="rw-cfg-chip">{v}</code
                                >{/each}
                        </span>
                    </div>
                {/if}
                {#if field.itemEnum}
                    <div class="rw-cfg-values">
                        <span class="rw-cfg-values-label">Item values</span>
                        <span class="rw-cfg-chips">
                            {#each field.itemEnum as v (v)}<code class="rw-cfg-chip">{v}</code
                                >{/each}
                        </span>
                    </div>
                {/if}

                {#if field.min !== undefined || field.max !== undefined}
                    <dl class="rw-cfg-meta">
                        <div>
                            <dt>Range</dt>
                            <dd>{field.min ?? "–"} to {field.max ?? "–"}</dd>
                        </div>
                    </dl>
                {/if}

                {#if field.nested}
                    <div class="rw-cfg-nested">
                        <div class="rw-cfg-nested-label"><code>{field.nestedLabel}</code></div>
                        {#each field.nested as sub (sub.id)}
                            {@render fieldRow(sub)}
                        {/each}
                    </div>
                {/if}
            </div>
        {/if}
    </div>
{/snippet}

{#snippet inline(text: string)}{#each inlineSegments(text) as seg, i (i)}{#if seg.href}<a
                class="rw-cfg-link"
                href={seg.href}>{seg.text}</a
            >{:else if seg.code}<code class="rw-cfg-inline-code">{seg.text}</code
            >{:else}{seg.text}{/if}{/each}{/snippet}

{#snippet proseNodes(topic: Topic)}
    {#if topic.body}
        {#each topic.body as node, i (i)}
            {#if "p" in node}
                <p class="rw-cfg-desc">{@render inline(node.p)}</p>
            {:else}
                <ul class="rw-cfg-topic-list">
                    {#each node.ul as item (item)}<li>{@render inline(item)}</li>{/each}
                </ul>
            {/if}
        {/each}
    {/if}
    {#if topic.example}
        <pre class="rw-cfg-example"><code>{topic.example}</code></pre>
    {/if}
{/snippet}

<!-- Always-open intro under a cluster header (topic.lead). -->
{#snippet leadBlock(topic: Topic)}
    <div class="rw-cfg-lead" id={topicElemId(topic)}>
        <p class="rw-cfg-desc"><strong>{topic.title}.</strong> {@render inline(topic.summary)}</p>
        {@render proseNodes(topic)}
    </div>
{/snippet}

<!-- A concept row: same interaction as a key row, visually distinct. -->
{#snippet topicRow(topic: Topic)}
    {@const id = topicElemId(topic)}
    <div class="rw-cfg-field rw-cfg-topic" {id} class:open={isOpen(id)}>
        <button
            type="button"
            class="rw-cfg-field-head"
            aria-expanded={isOpen(id)}
            onclick={() => toggle(id)}
        >
            <span class="rw-cfg-chevron" aria-hidden="true">▸</span>
            <span class="rw-cfg-topic-icon" aria-hidden="true">i</span>
            <span class="rw-cfg-topic-title">{topic.title}</span>
            <span class="rw-cfg-inline-desc">{plainText(topic.summary)}</span>
        </button>

        {#if isOpen(id)}
            <div class="rw-cfg-body" transition:slide={{ duration: 150 }}>
                <p class="rw-cfg-desc">{@render inline(topic.summary)}</p>
                {@render proseNodes(topic)}
            </div>
        {/if}
    </div>
{/snippet}

<style>
    .rw-cfg {
        margin-top: 1.5rem;
    }

    /* Sticky search + controls. */
    .rw-cfg-toolbar {
        position: sticky;
        top: 0;
        z-index: 2;
        display: flex;
        flex-wrap: wrap;
        align-items: center;
        gap: 0.75rem;
        padding: 0.75rem 0;
        margin-bottom: 1rem;
        background: var(--sl-color-bg);
        border-bottom: 1px solid var(--sl-color-hairline);
    }
    .rw-cfg-search {
        flex: 1 1 16rem;
        min-width: 12rem;
        padding: 0.5rem 0.75rem;
        font-size: var(--sl-text-sm);
        color: var(--sl-color-white);
        background: var(--sl-color-black);
        border: 1px solid var(--sl-color-gray-5);
        border-radius: 0.5rem;
    }
    .rw-cfg-search:focus {
        outline: 2px solid var(--sl-color-accent);
        outline-offset: 1px;
    }
    .rw-cfg-count {
        font-size: var(--sl-text-xs);
        color: var(--sl-color-gray-3);
        white-space: nowrap;
    }
    .rw-cfg-actions {
        display: flex;
        gap: 0.375rem;
    }
    .rw-cfg-actions button {
        padding: 0.375rem 0.625rem;
        font-size: var(--sl-text-xs);
        color: var(--sl-color-gray-1);
        background: var(--sl-color-gray-6);
        border: 1px solid var(--sl-color-gray-5);
        border-radius: 0.375rem;
        cursor: pointer;
    }
    .rw-cfg-actions button:hover:not(:disabled) {
        background: var(--sl-color-gray-5);
    }
    .rw-cfg-actions button:disabled {
        opacity: 0.4;
        cursor: not-allowed;
    }

    .rw-cfg-section {
        margin-bottom: 2rem;
    }
    .rw-cfg-section-header {
        margin: 0 0 0.25rem;
        font-size: var(--sl-text-h4);
    }
    .rw-cfg-section-header code {
        background: none;
        padding: 0;
    }
    .rw-cfg-section-desc {
        margin: 0 0 0.75rem;
        color: var(--sl-color-gray-2);
        font-size: var(--sl-text-sm);
    }
    .rw-cfg-empty {
        color: var(--sl-color-gray-3);
        font-size: var(--sl-text-sm);
    }

    /* Cluster: a labelled group of keys + topics within the table. */
    .rw-cfg-cluster {
        margin-bottom: 1.75rem;
    }
    .rw-cfg-cluster-title {
        margin: 0 0 0.5rem;
        font-size: var(--sl-text-sm);
        font-weight: 600;
        text-transform: uppercase;
        letter-spacing: 0.05em;
        color: var(--sl-color-gray-2);
    }

    /* Always-open intro paragraph under a cluster header. */
    .rw-cfg-lead {
        margin: 0 0 0.625rem;
        padding: 0.625rem 0.875rem;
        background: var(--sl-color-gray-6);
        border-left: 2px solid var(--sl-color-accent);
        border-radius: 0 0.4rem 0.4rem 0;
    }
    .rw-cfg-lead .rw-cfg-desc:first-child {
        margin-top: 0;
    }
    .rw-cfg-lead .rw-cfg-desc:last-child {
        margin-bottom: 0;
    }

    /* Concept row: same box as a key, tinted so it reads as prose not config. */
    .rw-cfg-topic {
        background: var(--sl-color-gray-6);
    }
    .rw-cfg-topic-icon {
        flex: none;
        display: inline-flex;
        align-items: center;
        justify-content: center;
        width: 1.1rem;
        height: 1.1rem;
        align-self: center;
        font-size: 0.7rem;
        font-style: italic;
        font-weight: 700;
        font-family: Georgia, serif;
        color: var(--sl-color-accent-high);
        border: 1px solid var(--sl-color-accent);
        border-radius: 50%;
    }
    .rw-cfg-topic-title {
        flex: none;
        font-weight: 600;
        color: var(--sl-color-white);
    }
    .rw-cfg-topic-list {
        margin: 0.4rem 0;
        padding-left: 1.1rem;
        font-size: var(--sl-text-sm);
        color: var(--sl-color-gray-1);
    }
    .rw-cfg-topic-list li {
        margin: 0.2rem 0;
    }

    .rw-cfg-fields {
        display: flex;
        flex-direction: column;
        gap: 0.375rem;
    }
    .rw-cfg-field {
        border: 1px solid var(--sl-color-gray-5);
        border-radius: 0.5rem;
        background: var(--sl-color-black);
        overflow: hidden;
    }
    .rw-cfg-field.open {
        border-color: var(--sl-color-accent);
    }

    .rw-cfg-field-head {
        display: flex;
        align-items: baseline;
        gap: 0.625rem;
        width: 100%;
        padding: 0.5rem 0.75rem;
        text-align: left;
        background: none;
        border: none;
        cursor: pointer;
        color: inherit;
        font: inherit;
    }
    .rw-cfg-field-head:hover {
        background: var(--sl-color-gray-6);
    }
    .rw-cfg-chevron {
        flex: none;
        color: var(--sl-color-gray-3);
        transition: transform 0.15s ease;
        transform: translateY(-1px);
    }
    .rw-cfg-field.open .rw-cfg-chevron {
        transform: translateY(-1px) rotate(90deg);
    }
    .rw-cfg-key {
        flex: none;
        font-weight: 600;
        background: none;
        padding: 0;
        color: var(--sl-color-white);
    }
    .rw-cfg-key mark {
        background: var(--sl-color-accent-low);
        color: var(--sl-color-white);
        border-radius: 2px;
    }
    .rw-cfg-type {
        flex: none;
        font-size: var(--sl-text-xs);
        font-family: var(--__sl-font-mono, monospace);
        color: var(--sl-color-accent-high);
    }
    .rw-cfg-badge {
        flex: none;
        font-size: 0.65rem;
        text-transform: uppercase;
        letter-spacing: 0.03em;
        padding: 0.05rem 0.35rem;
        border-radius: 0.25rem;
        background: var(--sl-color-orange-low);
        color: var(--sl-color-orange-high);
    }
    .rw-cfg-default {
        flex: none;
        font-size: var(--sl-text-xs);
        font-family: var(--__sl-font-mono, monospace);
        color: var(--sl-color-gray-3);
    }
    .rw-cfg-inline-desc {
        flex: 1 1 auto;
        min-width: 0;
        font-size: var(--sl-text-sm);
        color: var(--sl-color-gray-2);
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
    }
    .rw-cfg-field.open .rw-cfg-inline-desc {
        display: none;
    }

    .rw-cfg-body {
        padding: 0.25rem 0.75rem 0.875rem 2rem;
    }
    .rw-cfg-desc {
        margin: 0.25rem 0;
        font-size: var(--sl-text-sm);
        color: var(--sl-color-gray-1);
    }
    .rw-cfg-inline-code {
        font-size: 0.85em;
        padding: 0.05rem 0.3rem;
        border-radius: 0.25rem;
        background: var(--sl-color-gray-6);
        color: var(--sl-color-white);
    }
    .rw-cfg-hint {
        margin: 0.25rem 0;
        font-size: var(--sl-text-xs);
        color: var(--sl-color-gray-3);
    }
    .rw-cfg-link {
        font-family: var(--__sl-font-mono, monospace);
        font-size: 0.9em;
        color: var(--sl-color-accent-high);
        text-decoration-color: var(--sl-color-accent);
    }

    /* Cropped Web UI screenshot (group only). Light/dark swap on Starlight's
       html[data-theme]. */
    .rw-cfg-shot {
        margin: 0.6rem 0;
    }
    .rw-cfg-shot-img {
        display: block;
        width: 100%;
        max-width: 320px;
        border: 1px solid var(--sl-color-gray-5);
        border-radius: 0.5rem;
    }
    .rw-cfg-shot-dark {
        display: none;
    }
    :global([data-theme="dark"]) .rw-cfg-shot-light {
        display: none;
    }
    :global([data-theme="dark"]) .rw-cfg-shot-dark {
        display: block;
    }
    .rw-cfg-shot figcaption {
        margin-top: 0.35rem;
        font-size: var(--sl-text-xs);
        color: var(--sl-color-gray-3);
    }

    .rw-cfg-example {
        margin: 0.6rem 0;
        padding: 0.6rem 0.75rem;
        overflow-x: auto;
        font-size: var(--sl-text-xs);
        line-height: 1.55;
        background: var(--sl-color-gray-6);
        border: 1px solid var(--sl-color-gray-5);
        border-radius: 0.4rem;
    }
    .rw-cfg-example code {
        background: none;
        padding: 0;
        color: var(--sl-color-white);
        white-space: pre;
    }

    .rw-cfg-values {
        display: flex;
        flex-wrap: wrap;
        align-items: baseline;
        gap: 0.5rem;
        margin: 0.5rem 0;
    }
    .rw-cfg-values-label {
        font-size: var(--sl-text-xs);
        text-transform: uppercase;
        letter-spacing: 0.03em;
        color: var(--sl-color-gray-3);
    }
    .rw-cfg-chips {
        display: flex;
        flex-wrap: wrap;
        gap: 0.3rem;
    }
    .rw-cfg-chip {
        font-size: var(--sl-text-xs);
        padding: 0.1rem 0.45rem;
        border-radius: 0.3rem;
        background: var(--sl-color-gray-6);
        border: 1px solid var(--sl-color-gray-5);
        color: var(--sl-color-white);
    }

    /* "Examples" grid: value chip + plain-English meaning, one per row. */
    .rw-cfg-samples {
        margin: 0.6rem 0;
    }
    .rw-cfg-sample-list {
        display: grid;
        grid-template-columns: max-content 1fr;
        gap: 0.3rem 0.75rem;
        align-items: baseline;
        margin: 0.4rem 0 0;
        padding: 0;
        list-style: none;
    }
    .rw-cfg-sample-list li {
        display: contents;
    }
    .rw-cfg-sample-list li > .rw-cfg-chip {
        justify-self: start;
        white-space: nowrap;
    }
    .rw-cfg-sample-hint {
        font-size: var(--sl-text-sm);
        color: var(--sl-color-gray-2);
    }

    .rw-cfg-meta {
        display: flex;
        flex-wrap: wrap;
        gap: 0.25rem 1.5rem;
        margin: 0.5rem 0 0;
    }
    .rw-cfg-meta div {
        display: flex;
        gap: 0.4rem;
        align-items: baseline;
    }
    .rw-cfg-meta dt {
        font-size: var(--sl-text-xs);
        text-transform: uppercase;
        letter-spacing: 0.03em;
        color: var(--sl-color-gray-3);
    }
    .rw-cfg-meta dd {
        margin: 0;
        font-size: var(--sl-text-xs);
        color: var(--sl-color-gray-1);
    }

    .rw-cfg-nested {
        margin-top: 0.75rem;
        padding-left: 0.75rem;
        border-left: 2px solid var(--sl-color-gray-5);
    }
    .rw-cfg-nested-label {
        margin-bottom: 0.375rem;
        font-size: var(--sl-text-xs);
        color: var(--sl-color-gray-3);
    }
    .rw-cfg-nested-label code {
        background: none;
        padding: 0;
    }
    .rw-cfg-nested .rw-cfg-field {
        margin-bottom: 0.375rem;
    }
</style>
