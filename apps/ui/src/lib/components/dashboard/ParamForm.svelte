<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { TaskParam } from "@runwisp/common";
    import FormField from "@runwisp/ui/components/FormField.svelte";
    import Input from "@runwisp/ui/components/Input.svelte";
    import Select from "@runwisp/ui/components/Select.svelte";
    import Checkbox from "@runwisp/ui/components/Checkbox.svelte";
    import {
        isComboParam,
        paramIncluded,
        paramSupportsInclude,
        paramFieldError,
        resolveParamSupplied,
    } from "./param-form";

    // Sentinel for the "Custom…" entry of an allow_custom select. Choices
    // are arbitrary strings, so we mark the custom slot with a NUL-prefixed
    // value no choice can ever collide with (the daemon rejects a NUL byte in
    // any choice). Written as a \u0000 escape, not a literal NUL, so it stays
    // visible in source. Selecting it reveals the free-text input below.
    const CUSTOM_OPTION = "\u0000custom";

    // The form supplies *values* for parameters declared in runwisp.toml. It
    // never defines them — kinds, keys, choices and defaults all come from the
    // task definition. We mirror the daemon's resolve rules client-side so the
    // operator gets immediate feedback, but the daemon validates again.
    //
    // The emitted map is tri-state per key: a string passes that value (incl.
    // ""), `null` explicitly omits the parameter (the daemon does not re-inject
    // the default). The form is authoritative — it emits every declared key —
    // so a cleared field omits rather than silently falling back to the default.
    let {
        params,
        value = $bindable<Record<string, string | null>>({}),
        valid = $bindable(true),
    }: {
        params: TaskParam[];
        value?: Record<string, string | null>;
        valid?: boolean;
    } = $props();

    function initialValue(p: TaskParam): string {
        if (p.kind === "flag") return p.default === "true" ? "true" : "false";
        return p.default ?? "";
    }

    // Raw field state keyed by parameter identity. Flags hold "true"/"false";
    // everything else holds the entered string ("" means "not supplied").
    let vals = $state<Record<string, string>>(
        Object.fromEntries(params.map((p) => [p.key, initialValue(p)])),
    );
    let touched = $state<Record<string, boolean>>({});

    // Per free-text param, an explicit operator override of the include/omit
    // state. Unset = auto: the field follows its content (blank → omitted,
    // non-empty → included), tracked live as the operator types or clears.
    // Once the operator toggles the affordance, the choice sticks for that field.
    let includeOverride = $state<Record<string, boolean>>({});

    // Per allow_custom param, whether the selector sits on the custom slot (so
    // the free-text input shows). Seeded true when a default isn't a listed
    // choice, so a custom default opens in custom mode pre-filled.
    let customMode = $state<Record<string, boolean>>(
        Object.fromEntries(
            params.filter(isComboParam).map((p) => {
                const v = vals[p.key] ?? "";
                const isChoice = p.choices?.includes(v) ?? false;
                return [p.key, v !== "" && !isChoice];
            }),
        ),
    );

    // Effective inclusion for a field, threading the component's live state into
    // the pure rule (see param-form.ts).
    function isIncluded(p: TaskParam): boolean {
        return paramIncluded(p, vals[p.key] ?? "", includeOverride[p.key]);
    }

    function supportsInclude(p: TaskParam): boolean {
        return paramSupportsInclude(p, Boolean(customMode[p.key]));
    }

    function toggleInclude(p: TaskParam) {
        includeOverride[p.key] = !isIncluded(p);
        touched[p.key] = true;
    }

    function includeHint(p: TaskParam): string {
        if (!isIncluded(p)) return "Omitted (not passed) — include";
        if ((vals[p.key] ?? "") === "") return "Passing empty string — omit";
        return "Passing value — omit";
    }

    function fieldError(p: TaskParam): string {
        return paramFieldError(p, vals[p.key] ?? "", isIncluded(p));
    }

    function flagChecked(key: string): boolean {
        return vals[key] === "true";
    }

    function setFlag(key: string, checked: boolean) {
        vals[key] = checked ? "true" : "false";
    }

    function coerceString(v: unknown): string {
        return typeof v === "string" ? v : "";
    }

    const allValid = $derived(params.every((p) => fieldError(p) === ""));

    // The body sent to the daemon. Every declared key is emitted so the form is
    // authoritative: flags always carry "true"/"false"; an included value param
    // carries its (possibly empty) string; an omitted one carries `null` so the
    // daemon leaves it unset instead of re-injecting the declared default.
    const resolved = $derived.by(() => {
        const out: Record<string, string | null> = {};
        for (const p of params) {
            out[p.key] = resolveParamSupplied(p, vals[p.key] ?? "", isIncluded(p));
        }
        return out;
    });

    $effect(() => {
        value = resolved;
    });
    $effect(() => {
        valid = allValid;
    });

    function choiceOptions(choices: string[]): { value: string; label: string }[] {
        return choices.map((c) => ({ value: c, label: c }));
    }

    // comboOptions appends a "Custom…" entry after the declared choices.
    function comboOptions(choices: string[]): { value: string; label: string }[] {
        return [...choiceOptions(choices), { value: CUSTOM_OPTION, label: "Custom…" }];
    }

    // selectComboOption maps a select change to either custom mode or a chosen
    // value, mirroring the TUI's selector + custom slot.
    function selectComboOption(p: TaskParam, raw: unknown) {
        const sel = coerceString(raw);
        if (sel === CUSTOM_OPTION) {
            customMode[p.key] = true;
        } else {
            customMode[p.key] = false;
            vals[p.key] = sel;
            // Picking a concrete choice always passes it; drop any stale
            // include/omit override left over from the custom slot.
            delete includeOverride[p.key];
        }
        touched[p.key] = true;
    }
</script>

<div class="space-y-4">
    {#each params as p (p.key)}
        {#if p.kind === "flag"}
            <Checkbox
                label={p.key}
                description={p.description ?? ""}
                checked={flagChecked(p.key)}
                onchange={(e) => setFlag(p.key, e.currentTarget.checked)}
            />
        {:else if p.choices && p.choices.length > 0 && !p.allow_custom}
            <FormField
                label={p.key}
                required={p.required ?? false}
                description={p.description ?? ""}
            >
                <Select
                    value={vals[p.key] ?? ""}
                    options={choiceOptions(p.choices)}
                    placeholder="Select…"
                    error={touched[p.key] ? fieldError(p) : ""}
                    onchange={(v) => {
                        vals[p.key] = coerceString(v);
                        touched[p.key] = true;
                    }}
                />
            </FormField>
        {:else if p.choices && p.choices.length > 0 && p.allow_custom}
            <FormField
                label={p.key}
                required={p.required ?? false}
                description={p.description ?? ""}
            >
                <div class="space-y-2">
                    <Select
                        value={customMode[p.key] ? CUSTOM_OPTION : (vals[p.key] ?? "")}
                        options={comboOptions(p.choices)}
                        placeholder="Select…"
                        error={!customMode[p.key] && touched[p.key] ? fieldError(p) : ""}
                        onchange={(v) => selectComboOption(p, v)}
                    />
                    {#if customMode[p.key]}
                        <Input
                            value={vals[p.key] ?? ""}
                            placeholder="Custom value"
                            error={touched[p.key] ? fieldError(p) : undefined}
                            oninput={(e) => {
                                vals[p.key] = e.currentTarget.value;
                            }}
                            onblur={() => {
                                touched[p.key] = true;
                            }}
                        />
                        {@render includeToggle(p)}
                    {/if}
                </div>
            </FormField>
        {:else}
            <FormField
                label={p.key}
                required={p.required ?? false}
                description={p.description ?? ""}
            >
                <Input
                    type={p.type === "number" ? "number" : "text"}
                    value={vals[p.key] ?? ""}
                    error={touched[p.key] ? fieldError(p) : undefined}
                    oninput={(e) => {
                        vals[p.key] = e.currentTarget.value;
                    }}
                    onblur={() => {
                        touched[p.key] = true;
                    }}
                />
                {@render includeToggle(p)}
            </FormField>
        {/if}
    {/each}
</div>

{#snippet includeToggle(p: TaskParam)}
    {#if supportsInclude(p)}
        <button
            type="button"
            class="text-xs text-on-surface-faint transition-colors hover:text-on-surface-muted"
            onclick={() => toggleInclude(p)}
        >
            {includeHint(p)}
        </button>
    {/if}
{/snippet}
