<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import { Badge } from "@runwisp/ui";

    // A held task is loaded, listed, and shows its schedule, but the scheduler
    // stood down for it: a live cron daemon still owns the crontab it came from and
    // is still running it. Without this badge the task looks like any other
    // scheduled task while producing no runs at all, which is the one thing
    // RunWisp exists to make impossible. Cron is the only holder the API reports.
    const tooltip =
        "Held: a system cron daemon still owns this job, so RunWisp is not " +
        "running it; cron is. RunWisp records no history or output for it " +
        "until cron is retired. Run `sudo runwisp takeover` to hand it over, " +
        "or stop cron and disable it at boot (for example " +
        "`sudo systemctl disable --now cron`): RunWisp picks it up on its own " +
        "within a minute.";
</script>

<Badge variant="warning" size="sm" {tooltip}>held</Badge>
