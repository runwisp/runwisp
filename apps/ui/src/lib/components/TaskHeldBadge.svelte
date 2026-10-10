<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    // A held task is loaded, listed, and shows its schedule, but the scheduler
    // stood down for it: a live cron daemon still owns the crontab it came from and
    // is still running it. Without this badge the task looks like any other
    // scheduled task while producing no runs at all, which is the one thing
    // RunWisp exists to make impossible. Cron is the only holder the API reports.
    import { Badge } from "@runwisp/ui";
    import { HELD_BY_CRON_HELP } from "$lib/utils/task";
</script>

<Badge variant="warning" size="sm" tooltip={HELD_BY_CRON_HELP}>held</Badge>
