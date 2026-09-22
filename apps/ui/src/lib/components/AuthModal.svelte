<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import { tick } from "svelte";
    import { browser } from "$app/environment";
    import { Button, Input, Logo, Popover } from "@runwisp/ui";
    import { KeyRound, Lock } from "@lucide/svelte";
    import { authApi, RateLimitedError } from "$lib/api";
    import { authStore } from "$lib/stores";
    import { browserAuthEventBus } from "$lib/adapters/browser";
    import { createLogger } from "$lib/utils/logger";

    const logger = createLogger("AuthModal");

    // Env var / command rendered as inline chips inside the hint callout.
    const chip =
        "rounded-[3px] border border-outline bg-surface-sunken px-1 py-0.5 font-mono text-[0.7rem] text-on-surface";

    let isOpen = $state(false);
    let password = $state("");
    let error = $state("");
    let loading = $state(false);
    let authRequired = $state(true);

    $effect(() => {
        if (!browser) return;
        // Auth status is loaded once by the root layout; this modal only reacts
        // to authStore.current (see the effect below). Loading here too would
        // double the /api/auth/status hit and re-trigger the layout's auth
        // effect, re-seeding /api/system + /api/daemon a second time.
        const disposeAuthRequired = browserAuthEventBus.onAuthRequired(() => {
            // Already showing: any further 401 must not wipe what the operator
            // is typing.
            if (!authRequired || isOpen) {
                return;
            }
            logger.info("Authentication required");
            isOpen = true;
            error = "";
            password = "";
        });

        return () => {
            disposeAuthRequired();
        };
    });

    $effect(() => {
        const status = authStore.current;
        authRequired = status.required;

        if (!status.loaded) {
            return;
        }

        if (!status.required) {
            isOpen = false;
            error = "";
            password = "";
            return;
        }

        // Only show the login modal if unauthenticated (no valid cookie session
        // detected by the server).
        if (!status.authenticated) {
            isOpen = true;
        }
    });

    async function handleSubmit(e: SubmitEvent) {
        e.preventDefault();
        error = "";
        loading = true;

        try {
            // The login response sets the HttpOnly session cookie server-side;
            // the browser holds no copy of the JWT. We just flip local auth state.
            await authApi.login(password);
            authStore.markAuthenticated();
            isOpen = false;
            logger.info("Authentication successful");
            // Keep the password in the (now-removed) form until after it leaves
            // the DOM: password managers detect a successful login by the form
            // disappearing with its value intact, and offer to save it.
            await tick();
            password = "";
            // markAuthenticated() above flips authStore.current, which the root
            // layout's auth effect reacts to (connect streams, seed system, load
            // tasks/notifications). No separate success event needed.
        } catch (err) {
            if (err instanceof RateLimitedError) {
                error = "Too many attempts. Please wait a few minutes and try again.";
            } else {
                error = "Invalid password. Please try again.";
            }
            logger.error("Authentication failed", err);
        } finally {
            loading = false;
        }
    }
</script>

{#if isOpen}
    <!-- Purpose-built login gate (not the generic Modal): a non-closable, full
         viewport overlay that doubles as the mid-session re-auth screen. -->
    <div
        class="fixed inset-0 z-50 flex items-center justify-center p-4"
        role="dialog"
        aria-modal="true"
        aria-labelledby="auth-title"
    >
        <div class="absolute inset-0 bg-backdrop backdrop-blur-sm"></div>
        <!-- Subtle brand glow so the card doesn't read as floating on a void.
             --rw-primary-soft is teal at low alpha and adapts to both themes. -->
        <div
            class="pointer-events-none absolute inset-0"
            style="background: radial-gradient(55rem 55rem at 50% 26%, var(--rw-primary-soft), transparent 60%);"
        ></div>

        <div
            class="relative z-10 flex w-full max-w-sm flex-col rounded-[4px] border border-outline bg-surface-overlay p-8 shadow-lg"
        >
            <div class="flex flex-col items-center gap-3 text-center">
                <!-- The canonical brand lockup (same as the app nav): bare teal
                     mark beside the wordmark in the body sans at 700 — brand
                     voice, deliberately out of the mono chrome. -->
                <div class="flex items-center gap-1">
                    <Logo size="lg" />
                    <span
                        id="auth-title"
                        class="font-sans text-2xl font-bold tracking-[-0.02em] text-on-surface"
                    >
                        RunWisp
                    </span>
                </div>
            </div>

            <form onsubmit={handleSubmit} class="mt-6 space-y-4">
                <!-- Password managers key saved credentials to a username field.
                     There isn't one, so give them a stable, hidden one (per
                     Chromium's documented pattern) rather than none: without it,
                     managers either skip the save prompt or save duplicate
                     entries each time the daemon's random per-boot password
                     rotates. -->
                <input
                    type="text"
                    name="username"
                    autocomplete="username"
                    value="runwisp"
                    readonly
                    hidden
                />
                <Input
                    type="password"
                    name="password"
                    autocomplete="current-password"
                    aria-label="Password"
                    placeholder="Enter password"
                    bind:value={password}
                    error={error || undefined}
                    readonly={loading}
                    autofocus
                >
                    {#snippet leadingIcon()}
                        <Lock size={16} />
                    {/snippet}
                </Input>
                <Button
                    type="submit"
                    variant="primary"
                    size="lg"
                    fullWidth
                    {loading}
                    disabled={loading || !password}
                >
                    Login
                </Button>
            </form>

            <div class="mt-4 flex justify-center">
                <Popover placement="top">
                    {#snippet trigger()}
                        <span
                            class="inline-flex cursor-pointer items-center gap-1.5 rounded-[3px] px-2 py-1 font-sans text-xs text-on-surface-muted hover:text-primary"
                        >
                            <KeyRound size={13} />
                            Where's my password?
                        </span>
                    {/snippet}
                    <p class="max-w-[16rem] text-xs leading-relaxed text-on-surface-muted">
                        It's <code class={chip}>RUNWISP_PASSWORD</code> if that's set.
                        <br />Otherwise the daemon randomizes one each boot. Run
                        <code class={chip}>runwisp password</code> on the host to print it.
                    </p>
                </Popover>
            </div>
        </div>
    </div>
{/if}
