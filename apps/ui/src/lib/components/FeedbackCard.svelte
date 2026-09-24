<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import { Star, Bug, X } from "@lucide/svelte";
    import { slide } from "svelte/transition";
    import { Button, Textarea } from "@runwisp/ui";
    import { feedbackStore, systemStore } from "$lib/stores";
    import {
        MIN_UPTIME_MS,
        MOODS,
        REPO_URL,
        isPositive,
        sendFeedback,
        type FeedbackBody,
        type Mood,
    } from "./feedback";

    let step = $state<"rate" | "message" | "thanks">("rate");
    // raw: MOODS entries are compared by identity.
    let mood = $state.raw<Mood | null>(null);
    let message = $state("");
    let sending = $state(false);
    let error = $state("");

    let positive = $derived(mood ? isPositive(mood.rating) : true);

    // Pop up once the daemon has been up for an hour, even mid-session.
    // startedAt/checkUpdates only change on the one-shot /api/daemon seed.
    $effect(() => {
        const { startedAt, checkUpdates } = systemStore;
        if (!checkUpdates || startedAt === 0) return;
        const delay = Math.max(0, startedAt + MIN_UPTIME_MS - Date.now());
        const timer = setTimeout(() => {
            feedbackStore.autoOpen({ startedAt, checkUpdates });
        }, delay);
        return () => {
            clearTimeout(timer);
        };
    });

    async function submit(body: FeedbackBody, next: "message" | "thanks") {
        sending = true;
        error = "";
        const res = await sendFeedback(body, systemStore.version);
        sending = false;
        if (!res.ok) {
            error = res.error;
            return;
        }
        if ("rating" in body) feedbackStore.markSubmitted();
        step = next;
    }

    // close resets the card so reopening it from the footer starts over.
    function close() {
        feedbackStore.dismiss();
        step = "rate";
        mood = null;
        message = "";
        error = "";
    }

    function submitRating() {
        if (mood) void submit({ rating: mood.rating }, "message");
    }

    function submitMessage() {
        const text = message.trim();
        if (text) void submit({ message: text }, "thanks");
    }
</script>

{#snippet externalLink(href: string, label: string, primary: boolean)}
    <!-- External GitHub links, not app routes, so resolve() doesn't apply. -->
    <!-- eslint-disable svelte/no-navigation-without-resolve -->
    <a
        {href}
        target="_blank"
        rel="noreferrer"
        class={primary
            ? "inline-flex items-center justify-center gap-1.5 rounded-[3px] bg-primary px-2.5 py-1 font-mono text-xs font-medium text-on-primary hover:bg-primary-hover"
            : "text-xs text-primary hover:underline"}
    >
        {#if primary}
            {#if href.endsWith("/issues/new")}<Bug size={12} />{:else}<Star size={12} />{/if}
        {/if}
        {label}{primary ? "" : " ↗"}
    </a>
    <!-- eslint-enable svelte/no-navigation-without-resolve -->
{/snippet}

{#if systemStore.checkUpdates && feedbackStore.open}
    <section
        transition:slide={{ duration: 150 }}
        aria-label="Feedback"
        class="relative mx-3 mb-3 flex flex-col gap-2 rounded-[3px] border border-outline bg-surface-sunken/60 p-3 text-xs"
    >
        <button
            type="button"
            aria-label="Close feedback"
            class="absolute top-1.5 right-1.5 rounded-[3px] p-1 text-on-surface-faint hover:bg-surface-sunken hover:text-on-surface"
            onclick={close}
        >
            <X size={14} />
        </button>

        {#if step === "rate"}
            <p class="pr-5 font-medium text-on-surface">Enjoying RunWisp?</p>
            <div
                class="grid grid-cols-4 gap-1.5"
                role="group"
                aria-label="How do you feel about RunWisp?"
            >
                {#each MOODS as m (m.rating)}
                    <button
                        type="button"
                        title={m.label}
                        aria-label={m.label}
                        aria-pressed={mood === m}
                        class="rounded-[3px] border py-1.5 text-xl leading-none transition-transform hover:scale-110 {mood ===
                        m
                            ? 'border-primary bg-primary-soft'
                            : 'border-transparent'} {mood && mood !== m ? 'opacity-50' : ''}"
                        onclick={() => {
                            mood = m;
                            error = "";
                        }}
                    >
                        {m.emoji}
                    </button>
                {/each}
            </div>
            {#if mood}
                <div transition:slide={{ duration: 150 }}>
                    <Button size="xs" fullWidth loading={sending} onclick={submitRating}>
                        {sending ? "Sending…" : error ? "Try again" : "Submit"}
                    </Button>
                </div>
            {:else}
                <p class="text-on-surface-faint">One tap, anonymous. It really helps.</p>
            {/if}
        {:else if step === "message"}
            <p class="pr-5 font-medium text-on-surface">
                {positive
                    ? "Nice! What would make it even better?"
                    : "Sorry about that. What went wrong?"}
            </p>
            <Textarea
                bind:value={message}
                rows={3}
                maxlength={2000}
                resize="none"
                aria-label="Your feedback"
                placeholder={positive
                    ? "A missing feature, a rough edge…"
                    : "What were you trying to do?"}
                hint="Goes straight to the maintainers. Don't paste secrets."
            />
            <div class="flex items-center gap-2">
                <Button
                    size="xs"
                    class="flex-1"
                    loading={sending}
                    disabled={!message.trim()}
                    onclick={submitMessage}
                >
                    {sending ? "Sending…" : error ? "Try again" : "Submit"}
                </Button>
                <button
                    type="button"
                    class="px-1 text-on-surface-faint hover:text-on-surface"
                    onclick={() => {
                        step = "thanks";
                        error = "";
                    }}
                >
                    Skip
                </button>
            </div>
        {:else}
            <p class="pr-5 font-medium text-on-surface">Thanks, that means a lot!</p>
            {#if positive}
                <p class="text-on-surface-muted">
                    If RunWisp saves you time, a star on GitHub is the best way to help other people
                    find a small project like this.
                </p>
                {@render externalLink(REPO_URL, "Star on GitHub", true)}
                {@render externalLink(`${REPO_URL}/issues/new`, "Report a bug", false)}
            {:else}
                <p class="text-on-surface-muted">
                    Feedback here is anonymous, so we can't reply. If it's a bug, open a GitHub
                    issue and we'll follow up with you.
                </p>
                {@render externalLink(`${REPO_URL}/issues/new`, "Open an issue", true)}
            {/if}
        {/if}

        {#if error}
            <p role="alert" class="text-danger-soft-text">{error}</p>
        {/if}
    </section>
{/if}
