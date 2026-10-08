// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { format } from "node:util";

import { ownStack, type LineSink } from "./capture.ts";
import type { RunRequest, Session } from "./session.ts";

/** Where a run's output and exit code go: the session it came on. */
export type Reporter = Pick<Session, "output" | "exit">;

/** Runs one run's callback and resolves to its exit code. */
export type Serve = (request: RunRequest, signal: AbortSignal, sink: LineSink) => Promise<number>;

/** A unit's `on_overlap` and `max_concurrent`, which RunWisp enforces on the runs it can still see. */
export interface OverlapPolicy {
    onOverlap: "queue" | "skip" | "kill";
    maxConcurrent: number;
}

/** A callback running in this process; it outlives its run when it ignores the stop signal. */
interface Execution {
    session: Reporter;
    run: string;
    abort: AbortController;
    done: Promise<void>;
}

/**
 * Runs the callbacks of the runs RunWisp sends this app. A run ends for
 * RunWisp when the callback returns, or when it ignores a stop past
 * graceful_stop; the callback itself can only be asked to stop, through its
 * signal.
 */
export class Runner {
    private readonly serve: Serve;
    private readonly policy: (task: string) => OverlapPolicy;
    /** By task name. */
    private readonly running = new Map<string, Execution[]>();
    private closing = false;

    constructor(serve: Serve, policy: (task: string) => OverlapPolicy) {
        this.serve = serve;
        this.policy = policy;
    }

    start(session: Reporter, request: RunRequest): void {
        if (this.closing) {
            session.output(request.run, "stderr", "runwisp: the app is shutting down");
            session.exit(request.run, 1);
            return;
        }
        const abort = new AbortController();
        void this.execute(session, request, abort, (code) => {
            // A stopped run exits like a killed process, whatever the callback returned.
            session.exit(request.run, abort.signal.aborted ? 143 : code);
        });
    }

    stop(run: string): void {
        this.abortWhere((execution) => execution.run === run);
    }

    /** The session closed: RunWisp has ended its runs, so ask their callbacks to stop. */
    disconnected(session: Reporter): void {
        this.abortWhere((execution) => execution.session === session);
    }

    private abortWhere(match: (execution: Execution) => boolean): void {
        for (const executions of this.running.values()) {
            for (const execution of executions) if (match(execution)) execution.abort.abort();
        }
    }

    /** Asks every run of the given tasks to stop. */
    stopTasks(tasks: Iterable<string>): void {
        for (const task of tasks) {
            for (const execution of this.running.get(task) ?? []) execution.abort.abort();
        }
    }

    /** Takes no more runs, and resolves once no callback is running. */
    async close(): Promise<void> {
        this.closing = true;
        while (this.running.size > 0) {
            await Promise.all([...this.running.values()].flat().map((e) => e.done));
        }
    }

    /**
     * The app is about to die: say why in every run it takes down, since the
     * stack trace would otherwise only reach the app's own stderr. Observes
     * only; Node still crashes the app as usual.
     */
    readonly reportCrash = (err: unknown): void => {
        // ponytail: relies on small Unix socket writes completing synchronously, before the process exits.
        for (const executions of this.running.values()) {
            for (const { session, run } of executions) {
                session.output(
                    run,
                    "stderr",
                    "runwisp: the app crashed while this run was in progress:",
                );
                for (const line of format(ownStack(err)).split("\n"))
                    session.output(run, "stderr", line);
            }
        }
    };

    /** Runs the callback and reports its exit code, before the run counts as done. */
    private async execute(
        session: Reporter,
        request: RunRequest,
        abort: AbortController,
        exit: (code: number) => void,
    ): Promise<void> {
        const { task, run } = request;
        const say = (line: string) => {
            session.output(run, "stderr", `runwisp: ${line}`);
        };
        let executions = this.running.get(task) ?? [];
        // RunWisp only sends a run once the previous ones ended, so anything
        // still here ignored its stop signal: hold to the unit's overlap policy.
        const { onOverlap, maxConcurrent } = this.policy(task);
        if (executions.length >= maxConcurrent && onOverlap !== "kill") {
            if (onOverlap === "skip") {
                say(
                    "skipped: the previous run is still running in the app after RunWisp stopped it",
                );
                exit(1);
                return;
            }
            say("waiting for the previous run, which ignored its stop signal, to finish");
            const aborted = new Promise<void>((resolve) => {
                abort.signal.addEventListener("abort", () => {
                    resolve();
                });
            });
            while (executions.length >= maxConcurrent && !abort.signal.aborted) {
                await Promise.race([aborted, ...executions.map((e) => e.done)]);
                // The last previous run removes the list when it ends, and a
                // later run may have started a new one meanwhile.
                executions = this.running.get(task) ?? [];
            }
            if (abort.signal.aborted) {
                exit(143);
                return;
            }
        }

        let finish: () => void = () => undefined;
        const done = new Promise<void>((resolve) => (finish = resolve));
        const execution: Execution = { session, run, abort, done };
        this.running.set(task, executions);
        executions.push(execution);
        let code = 1;
        try {
            code = await this.serve(request, abort.signal, (stream, line) => {
                session.output(run, stream, line);
            });
        } finally {
            executions.splice(executions.indexOf(execution), 1);
            if (executions.length === 0) this.running.delete(task);
            exit(code);
            finish();
        }
    }
}
