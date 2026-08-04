<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import { Badge } from "@runwisp/ui";

    interface Props {
        /** Why the task is held. Only "cron" exists today. */
        heldBy: "cron";
    }

    let { heldBy }: Props = $props();

    // A held task is loaded, listed, and shows its schedule, but the scheduler
    // stood down for it: a live cron daemon still owns the crontab it came from and
    // is still running it. Without this badge the task looks like any other
    // scheduled task while producing no runs at all — which is the one thing
    // RunWisp exists to make impossible.
    const tooltip = $derived(
        heldBy === "cron"
            ? "Held: a system cron daemon still owns this job, so RunWisp is not " +
                  "running it — cron is. RunWisp records no history or output for it " +
                  "until cron is retired. Run `sudo runwisp takeover` to hand it over, " +
                  "or just stop cron — RunWisp picks it up on its own within a minute."
            : "Held: RunWisp is not scheduling this task.",
    );
</script>

<span title={tooltip}>
    <Badge variant="warning" size="sm">held</Badge>
</span>
