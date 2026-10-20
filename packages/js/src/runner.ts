// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { describeError, type LineSink } from "./capture.ts";
import type { EndReason, RunRequest, Session } from "./session.ts";

/** Where a run's output and exit code go: the session it came on. */
export type Reporter = Pick<Session, "output" | "exit" | "flush">;

/**
 * Runs one run's callback and resolves to its exit code. signal asks it to
 * stop; kill aborts once RunWisp gave up on it after graceful_stop, for what
 * can be killed (a task file's process).
 */
export type Serve = (
    request: RunRequest,
    signal: AbortSignal,
    sink: LineSink,
    kill: AbortSignal,
) => Promise<number>;

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
    kill: AbortController;
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
    /** Runs waiting for a previous run that ignored its stop signal: they can be stopped too. */
    private readonly waiting = new Set<Pick<Execution, "session" | "run" | "abort">>();
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
        void this.execute(session, request, abort, (code, reason) => {
            // A stopped run exits like a killed process, whatever the callback returned.
            session.exit(request.run, abort.signal.aborted ? 143 : code, reason);
        });
    }

    stop(run: string): void {
        this.abortWhere((execution) => execution.run === run);
    }

    /** RunWisp gave up on the run after graceful_stop: kill what can be killed of it. */
    kill(run: string): void {
        this.stop(run);
        for (const executions of this.running.values()) {
            for (const execution of executions) if (execution.run === run) execution.kill.abort();
        }
    }

    /** How many callbacks are running, ones that outlived their run included. */
    get size(): number {
        let size = 0;
        for (const executions of this.running.values()) size += executions.length;
        return size;
    }

    /** The session closed: RunWisp has ended its runs, so ask their callbacks to stop. */
    disconnected(session: Reporter): void {
        this.abortWhere((execution) => execution.session === session);
    }

    private abortWhere(match: (execution: Pick<Execution, "session" | "run">) => boolean): void {
        for (const executions of this.running.values()) {
            for (const execution of executions) if (match(execution)) execution.abort.abort();
        }
        for (const execution of this.waiting) if (match(execution)) execution.abort.abort();
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
        const lines = describeError(err).split("\n");
        for (const executions of this.running.values()) {
            for (const { session, run } of executions) {
                session.output(
                    run,
                    "stderr",
                    "runwisp: the app crashed while this run was in progress:",
                );
                for (const line of lines) session.output(run, "stderr", line);
                session.flush();
            }
        }
    };

    /**
     * Waits until fewer than maxConcurrent callbacks of task run, or the
     * waiting run is stopped, or the runner closes. Returns the task's
     * running list as it is then.
     */
    private async waitForTurn(
        task: string,
        maxConcurrent: number,
        waiter: Pick<Execution, "session" | "run" | "abort">,
    ): Promise<Execution[]> {
        const { signal } = waiter.abort;
        const aborted = new Promise<void>((resolve) => {
            signal.addEventListener("abort", () => {
                resolve();
            });
        });
        let executions = this.running.get(task) ?? [];
        this.waiting.add(waiter);
        try {
            while (executions.length >= maxConcurrent && !signal.aborted && !this.closing) {
                await Promise.race([aborted, ...executions.map((e) => e.done)]);
                // The last previous run removes the list when it ends, and a
                // later run may have started a new one meanwhile.
                executions = this.running.get(task) ?? [];
            }
        } finally {
            this.waiting.delete(waiter);
        }
        return executions;
    }

    /** Runs the callback and reports its exit code, before the run counts as done. */
    private async execute(
        session: Reporter,
        request: RunRequest,
        abort: AbortController,
        exit: (code: number, reason?: EndReason) => void,
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
                exit(1, "skipped");
                return;
            }
            say("waiting for the previous run, which ignored its stop signal, to finish");
            executions = await this.waitForTurn(task, maxConcurrent, { session, run, abort });
            if (abort.signal.aborted) {
                say(
                    "this run never started: the previous run ignored its stop signal and still runs",
                );
                exit(143);
                return;
            }
            if (this.closing) {
                say("the app is shutting down");
                exit(1);
                return;
            }
        }

        let finish: () => void = () => undefined;
        const done = new Promise<void>((resolve) => (finish = resolve));
        const kill = new AbortController();
        const execution: Execution = { session, run, abort, kill, done };
        this.running.set(task, executions);
        executions.push(execution);
        let code = 1;
        try {
            code = await this.serve(
                request,
                abort.signal,
                (stream, line, partial) => {
                    session.output(run, stream, line, partial);
                },
                kill.signal,
            );
        } catch (err) {
            // serve reports the callback's own failures; this is anything else, which must not crash the app.
            say(describeError(err));
        } finally {
            executions.splice(executions.indexOf(execution), 1);
            if (executions.length === 0) this.running.delete(task);
            exit(code);
            finish();
        }
    }
}
