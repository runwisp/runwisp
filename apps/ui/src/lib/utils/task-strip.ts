// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { isService, type Run, type Task } from "@runwisp/common";
import {
    formatDayMonth,
    formatRelativeTime,
    formatRelativeTimeWithAbsolute,
    humanizeCron,
    runDuration,
} from "@runwisp/ui";
import { canTogglePause, hasCron, isServiceStopped, taskInstanceCount } from "./task";

/** Which state the badge names; each gets its own colour and icon (TaskStateBadge). */
export type StripBadgeKind =
    | "scheduled"
    | "manual"
    | "station"
    | "running"
    | "starting"
    | "up"
    | "failed"
    | "down"
    | "paused"
    | "held"
    | "stopped";

/** A piece of the strip's sentence; muted pieces are the asides. */
export interface StripText {
    text: string;
    muted?: boolean;
    struck?: boolean;
    mono?: boolean;
}

export type StripActionKind = "run" | "pause" | "resume" | "start" | "restart" | "stop-service";

export interface StripAction {
    kind: StripActionKind;
    label: string;
    title: string;
    /** The one filled button: the state's next step. At most one per strip. */
    primary: boolean;
}

export interface TaskStripModel {
    /** The state word. With `runId` it opens that run. */
    badge: { text: string; kind: StripBadgeKind; runId?: string; title?: string };
    /** What happens next, or why not. */
    sentence: StripText[];
    /** The same, short, for the folded strip. */
    short: StripText[];
    /** A link after the sentence: open a run, or how to hand a held job over. */
    link?: { text: string; runId?: string; held?: true };
    actions: StripAction[];
}

export interface TaskStripInput {
    task: Task;
    /** The newest running run of this task, if any. */
    running: Run | undefined;
    /** False in station mode: the station schedules, this daemon only runs. */
    schedulingActive: boolean;
    now: Date;
}

const t = (text: string): StripText => ({ text });
const m = (text: string): StripText => ({ text, muted: true });

function runAction(task: Task, primary: boolean, here = false): StripAction {
    const params = (task.parameters ?? []).length > 0;
    if (here) {
        return {
            kind: "run",
            label: "Run here",
            title: "Run it on this machine now; scheduling stays with the station",
            primary,
        };
    }
    return {
        kind: "run",
        label: params ? "Run…" : "Run now",
        title: params ? "Pick parameters and run it now" : "Run it now",
        primary,
    };
}

function runningBadge(run: Run, now: Date): TaskStripModel["badge"] {
    const took = runDuration(run, now.getTime());
    return {
        text: took ? `Running · ${took}` : "Running",
        kind: "running",
        runId: run.id,
        title: "Open the running run",
    };
}

function failedBadge(task: Task): TaskStripModel["badge"] | undefined {
    const last = task.lastRun;
    if (last?.status !== "ended" || !last.isFailure) return undefined;
    return {
        text: "Last run failed",
        kind: "failed",
        runId: last.id,
        title: "Open the failed run",
    };
}

function cronStrip({ task, schedulingActive, now }: TaskStripInput): TaskStripModel {
    const schedule = humanizeCron(task.cron ?? "").humanized;
    const run = task.manualTrigger ? [runAction(task, true)] : [];
    if (task.pausedAt) {
        return {
            badge: { text: "Paused", kind: "paused" },
            sentence: [
                t(`since ${formatDayMonth(task.pausedAt)}`),
                m(" · "),
                { text: schedule, muted: true, struck: true },
                m(" is skipped, manual runs still work"),
            ],
            short: [t(`since ${formatDayMonth(task.pausedAt)}`), m(" · schedule skipped")],
            // Same places as Run now and Pause, so the buttons don't swap.
            actions: canTogglePause(task)
                ? [
                      ...run.map((a) => ({ ...a, primary: false })),
                      {
                          kind: "resume",
                          label: "Resume schedule",
                          title: "Resume the cron schedule",
                          primary: true,
                      },
                  ]
                : run,
        };
    }
    if (task.heldBy) {
        return {
            badge: { text: "Held by cron", kind: "held" },
            sentence: [
                t("A system cron daemon still runs this job, so RunWisp records nothing for it."),
            ],
            short: [t("system cron still runs this job")],
            link: { text: "How to hand it over", held: true },
            actions: run.map((a) => ({ ...a, primary: false })),
        };
    }
    if (!schedulingActive) {
        return {
            badge: { text: "Scheduled by Station", kind: "station" },
            sentence: [t(schedule), m(" · RunWisp Station decides when it runs")],
            short: [t("Station decides when it runs")],
            actions: task.manualTrigger ? [runAction(task, true, true)] : [],
        };
    }
    const next = task.nextRunAt;
    const pause: StripAction[] = canTogglePause(task)
        ? [
              {
                  kind: "pause",
                  label: "Pause",
                  title: "Pause the cron schedule. Skipped ticks aren't caught up; manual runs still work.",
                  primary: false,
              },
          ]
        : [];
    return {
        badge: failedBadge(task) ?? { text: "Scheduled", kind: "scheduled" },
        sentence: next
            ? [t(schedule), m(" · "), t(`next run ${formatRelativeTimeWithAbsolute(next, now)}`)]
            : [t(schedule)],
        short: next ? [t(`next ${formatRelativeTime(next, now)}`)] : [t(schedule)],
        actions: [...run, ...pause],
    };
}

function manualStrip({ task }: TaskStripInput): TaskStripModel {
    return {
        badge: failedBadge(task) ?? { text: "Manual only", kind: "manual" },
        sentence: [
            t("Runs only when triggered: here, "),
            { text: "runwisp run", mono: true },
            t(", or the REST API"),
        ],
        short: [t("runs when triggered")],
        actions: task.manualTrigger ? [runAction(task, true)] : [],
    };
}

/** " · last exit 137", or nothing when the last exit was a signal (-1) or unknown. */
function lastExit(code: number | undefined): string {
    return code !== undefined && code >= 0 ? `, last exit ${String(code)}` : "";
}

type Instance = NonNullable<NonNullable<Task["service"]>["instances"]>[number];

// What every service state needs: the counts, the newest run, and the
// actions, empty when manual_trigger locks the service to its policy.
interface ServiceCtx {
    task: Task;
    desired: number;
    up: number;
    lastRunId: string | undefined;
    restart: StripAction;
    stop: StripAction;
    start: (label?: string) => StripAction;
    allow: (actions: StripAction[]) => StripAction[];
}

function serviceCtx(task: Task): ServiceCtx {
    const desired = taskInstanceCount(task);
    return {
        task,
        desired,
        up: task.service?.runningInstances ?? 0,
        lastRunId: task.lastRun?.id,
        restart: {
            kind: "restart",
            label: "Restart",
            title:
                desired > 1
                    ? `Cancel and restart all ${String(desired)} instances, healthy ones included`
                    : "Cancel and restart it",
            primary: false,
        },
        stop: {
            kind: "stop-service",
            label: "Stop service",
            title: task.autostart
                ? "Stop it until you start it again or the daemon restarts"
                : "Stop it until you start it again",
            primary: false,
        },
        start: (label = "Start") => ({
            kind: "start",
            label,
            title: "Start the instances that aren't running; running ones are left alone",
            primary: true,
        }),
        allow: (actions) => (task.manualTrigger ? actions : []),
    };
}

/** "Instance #2" on a multi-instance service, "It" on a single one. */
const instanceName = (c: ServiceCtx, index: number) =>
    c.desired > 1 ? `Instance #${String(index + 1)}` : "It";

function withRun(c: ServiceCtx, badge: TaskStripModel["badge"]): TaskStripModel["badge"] {
    return c.lastRunId === undefined ? badge : { ...badge, runId: c.lastRunId };
}

function runLink(c: ServiceCtx, text: string): Pick<TaskStripModel, "link"> {
    return c.lastRunId === undefined ? {} : { link: { text, runId: c.lastRunId } };
}

function degradedBadge(c: ServiceCtx, title: string): TaskStripModel["badge"] {
    const text = c.desired > 1 ? `${String(c.up)} of ${String(c.desired)} up` : "Down";
    return withRun(c, { text, kind: "down", title });
}

function stoppedStrip(c: ServiceCtx): TaskStripModel {
    const auto = c.task.autostart;
    return {
        badge: { text: "Stopped", kind: "stopped" },
        sentence: [
            t("Stays down until you start it"),
            auto
                ? m(" or the daemon restarts (autostart is on)")
                : m(" · autostart is off, so a daemon restart leaves it down"),
        ],
        short: [t(auto ? "until started or daemon restart" : "stays down until started")],
        actions: c.allow([c.start()]),
    };
}

function allGaveUpStrip(c: ServiceCtx, first: Instance): TaskStripModel {
    const each = c.desired > 1 ? " each" : "";
    return {
        badge: withRun(c, {
            text: "Down",
            kind: "down",
            title: "RunWisp won't restart it on its own",
        }),
        sentence: [
            t(c.desired > 1 ? `All ${String(c.desired)} instances gave up` : "It gave up"),
            m(` · ${String(first.startFails)} failed starts${each}${lastExit(first.lastExitCode)}`),
        ],
        short: [t(`gave up after ${String(first.startFails)} failed starts`)],
        ...runLink(c, "Open the last run"),
        actions: c.allow([c.start()]),
    };
}

function gaveUpStrip(c: ServiceCtx, fatal: Instance, count: number): TaskStripModel {
    const slot = `#${String(fatal.index + 1)}`;
    const label = count === 1 && c.desired > 1 ? `Start ${slot}` : "Start";
    return {
        badge: degradedBadge(c, "RunWisp won't restart it on its own"),
        sentence: [
            t(`${instanceName(c, fatal.index)} gave up`),
            m(` · ${String(fatal.startFails)} failed starts${lastExit(fatal.lastExitCode)}`),
        ],
        short: [t(`${slot} gave up after ${String(fatal.startFails)} failed starts`)],
        ...runLink(c, "Open its last run"),
        actions: c.allow([c.start(label), c.restart, c.stop]),
    };
}

function restartingStrip(c: ServiceCtx, retrying: Instance): TaskStripModel {
    // RunWisp is still on it: nothing is filled, reading the log is the next step.
    const limit = c.task.restartAttempts ?? 0;
    const of = limit > 0 ? ` of ${String(limit)}` : "";
    // The supervisor gives up once failures pass restart_attempts, so the
    // pending start is restart number startFails (1 after a healthy crash).
    const attempt = `restart ${String(Math.max(1, retrying.startFails))}${of}`;
    const slot = c.desired > 1 ? `#${String(retrying.index + 1)} ` : "";
    return {
        badge: degradedBadge(c, "RunWisp is restarting it"),
        sentence: [
            t(`${instanceName(c, retrying.index)} is restarting`),
            m(` · ${attempt}${lastExit(retrying.lastExitCode)}`),
        ],
        short: [t(`${slot}restarting, ${attempt}`)],
        ...runLink(c, "Open its last run"),
        actions: c.allow([c.restart, c.stop]),
    };
}

function upStrip(c: ServiceCtx, instances: Instance[], now: Date): TaskStripModel {
    const since = instances
        .map((i) => i.startedAt)
        .filter((s) => s !== undefined)
        .sort()[0];
    const multi = c.desired > 1;
    const count = multi ? `${String(c.up)} of ${String(c.desired)} instances up` : "Up";
    const live = c.up > 0 || c.task.service === undefined;
    const model: TaskStripModel = {
        badge: { text: live ? "Running" : "Starting", kind: live ? "up" : "starting" },
        sentence: [t(count)],
        short: [t(count)],
        actions: c.allow([c.restart, c.stop]),
    };
    if (since === undefined) return model;
    const ago = formatRelativeTime(since, now);
    return {
        ...model,
        sentence: [t(count), m(` · since ${formatDayMonth(since)}, ${ago}`)],
        short: [
            t(multi ? `${String(c.up)} of ${String(c.desired)} up` : "up"),
            m(` · since ${ago}`),
        ],
    };
}

function serviceStrip({ task, now }: TaskStripInput): TaskStripModel {
    const c = serviceCtx(task);
    if (isServiceStopped(task)) return stoppedStrip(c);
    const instances = task.service?.instances ?? [];
    const gaveUp = instances.filter((i) => i.state === "fatal");
    const [fatal] = gaveUp;
    if (fatal && gaveUp.length === c.desired) return allGaveUpStrip(c, fatal);
    if (fatal) return gaveUpStrip(c, fatal, gaveUp.length);
    const retrying = instances.find((i) => i.state === "restarting");
    if (retrying) return restartingStrip(c, retrying);
    return upStrip(c, instances, now);
}

/** The task strip: one state word, one sentence, and the task's actions. */
export function taskStrip(input: TaskStripInput): TaskStripModel {
    const { task, running, now } = input;
    if (isService(task.kind)) return serviceStrip(input);
    const model = hasCron(task) ? cronStrip(input) : manualStrip(input);
    // A run in flight outranks every other state word.
    return running ? { ...model, badge: runningBadge(running, now) } : model;
}
