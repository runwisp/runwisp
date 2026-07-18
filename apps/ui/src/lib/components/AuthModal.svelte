<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { browser } from "$app/environment";
    import { Button, Input, Logo, Modal } from "@runwisp/ui";
    import { authApi, RateLimitedError } from "$lib/api";
    import { authStore } from "$lib/stores";
    import { browserTokenStorage, browserAuthEventBus } from "$lib/adapters/browser";
    import { createLogger } from "$lib/utils/logger";

    const logger = createLogger("AuthModal");

    let isOpen = $state(false);
    let password = $state("");
    let error = $state("");
    let loading = $state(false);
    let authRequired = $state(true);

    $effect(() => {
        if (!browser) return;
        void authStore.load();

        const disposeAuthRequired = browserAuthEventBus.onAuthRequired(() => {
            if (!authRequired) {
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

        // Only show the login modal if unauthenticated (no localStorage token
        // AND no valid cookie session detected by the server).
        if (!status.authenticated) {
            isOpen = true;
        }
    });

    async function handleSubmit(e: SubmitEvent) {
        e.preventDefault();
        error = "";
        loading = true;

        try {
            const data = await authApi.login(password);
            browserTokenStorage.setToken(data.token);
            authStore.markAuthenticated();
            isOpen = false;
            password = "";
            logger.info("Authentication successful");

            browserAuthEventBus.emitAuthSuccess();
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

<Modal
    bind:open={isOpen}
    title="Authentication Required"
    description="Enter your password to access this RunWisp instance."
    size="sm"
    closable={false}
>
    <div class="flex flex-col items-center gap-4">
        <div
            class="flex h-16 w-16 items-center justify-center rounded-2xl bg-primary-soft ring-1 ring-outline"
        >
            <Logo size="lg" />
        </div>
        <form onsubmit={handleSubmit} class="w-full space-y-4">
            <Input
                type="password"
                label="Password"
                placeholder="Enter password"
                bind:value={password}
                error={error || undefined}
                disabled={loading}
                autofocus
            />
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
            <p class="text-xs text-on-surface-muted">
                Your password comes from <code class="font-mono">RUNWISP_PASSWORD</code> if it's
                set. Otherwise the daemon picks a random one each boot — run
                <code class="font-mono">runwisp password</code> on the host to print it.
            </p>
        </form>
    </div>
</Modal>
