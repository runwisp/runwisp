<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { RefreshCw, WifiOff, LoaderCircle } from "@lucide/svelte";
    import { Button, formatDuration } from "@runwisp/ui";
    import { connectionStore } from "$lib/stores";
    import { getApiUrl } from "$lib/utils/env";

    const apiUrl = getApiUrl();
    const endpoint = apiUrl.trim() === "" ? "this site's origin" : apiUrl;

    const c = connectionStore;
    let reconnecting = $derived(c.isRetrying || c.status === "connecting");

    function elapsedSince(epoch: number | null): string | null {
        return epoch === null ? null : formatDuration(Math.max(0, c.now - epoch));
    }

    let downFor = $derived(elapsedSince(c.disconnectedSince));
    let lastSeen = $derived(c.status !== "connected" ? elapsedSince(c.lastConnectedAt) : null);

    let retryLine = $derived.by(() => {
        if (reconnecting) return "Trying to reconnect…";
        if (c.nextRetryAt === null) return null;
        const seconds = Math.max(0, Math.ceil((c.nextRetryAt - c.now) / 1000));
        return `Next automatic retry in ${seconds}s`;
    });
</script>

<div
    class="flex flex-col items-center justify-center gap-5 rounded-xl border border-danger-200 bg-danger-50/40 px-6 py-12 text-center"
    role="alert"
    aria-live="polite"
>
    <div class="rounded-full bg-danger-soft p-3">
        {#if reconnecting}
            <LoaderCircle class="h-8 w-8 animate-spin text-danger-surface" />
        {:else}
            <WifiOff class="h-8 w-8 text-danger-surface" />
        {/if}
    </div>

    <div class="max-w-md space-y-2">
        <h3 class="text-lg font-semibold text-on-surface">Connection Lost</h3>
        <p class="text-sm text-on-surface-muted">
            The UI can't reach the runner API at <span
                class="rounded bg-surface-sunken px-1.5 py-0.5 font-mono text-xs text-on-surface"
                >{endpoint}</span
            >. The daemon may be restarting or your network is down.
        </p>
    </div>

    <dl
        class="grid w-full max-w-md grid-cols-2 gap-x-6 gap-y-2 rounded-lg border border-outline bg-surface-raised px-5 py-3 text-left text-xs"
    >
        {#if downFor !== null}
            <dt class="text-on-surface-muted">Down for</dt>
            <dd class="text-right font-medium text-on-surface tabular-nums">{downFor}</dd>
        {/if}
        {#if lastSeen !== null}
            <dt class="text-on-surface-muted">Last reached</dt>
            <dd class="text-right font-medium text-on-surface tabular-nums">{lastSeen} ago</dd>
        {/if}
        {#if c.retryAttempts > 0}
            <dt class="text-on-surface-muted">Attempts</dt>
            <dd class="text-right font-medium text-on-surface tabular-nums">{c.retryAttempts}</dd>
        {/if}
        {#if retryLine !== null}
            <dt class="text-on-surface-muted">Status</dt>
            <dd class="text-right font-medium text-on-surface">{retryLine}</dd>
        {/if}
    </dl>

    <Button variant="secondary" onclick={c.retryNow} loading={reconnecting}>
        {#snippet icon()}<RefreshCw size={16} />{/snippet}
        Retry now
    </Button>

    {#if c.lastError}
        <details class="w-full max-w-md text-left">
            <summary class="cursor-pointer text-xs text-on-surface-muted hover:text-on-surface"
                >Details</summary
            >
            <pre
                class="mt-2 overflow-x-auto rounded-md bg-surface-sunken p-3 font-mono text-[11px] text-on-surface-muted">{c.lastError}</pre>
        </details>
    {/if}
</div>
