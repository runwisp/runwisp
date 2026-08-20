// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"log/slog"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/datadir"
	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runtime"
	"github.com/spf13/cobra"
)

var runFlags struct {
	Daemon     bool
	Standalone bool
	URL        string
	Password   string
	Detach     bool
	JSON       bool
}

// runLineOut is where a run's stdout-stream log lines are written. In --json
// mode they go to stderr so stdout carries only the final runJSONDoc; stderr-
// stream lines always go to os.Stderr regardless.
func runLineOut() io.Writer {
	if runFlags.JSON {
		return os.Stderr
	}
	return os.Stdout
}

// runCmd runs a task and streams its output to stdout/stderr. It auto-detects
// whether a daemon owns this data dir: if one is running, the request is
// dispatched over the REST API; otherwise the task runs in-process. The user
// can pin the choice with --daemon or --standalone, or target a remote daemon
// over the network with --url.
var runCmd = &cobra.Command{
	Use:   "run <task-name>",
	Short: "Run a task and stream its output",
	Long: `Runs the named task and streams its log lines to stdout/stderr. Exits with
the task's exit code.

With --url (or RUNWISP_URL) set, the run is dispatched to a remote daemon over
the network: RunWisp logs in (CHAP), triggers the task, follows its live log
stream, and exits with the task's exit code — ideal for automation scripts and
CI. The password comes from --password or RUNWISP_PASSWORD, and the resulting
session token is cached so repeated calls don't re-authenticate.

Without --url, the run is local. If a daemon is running against the same data
dir, ` + "`runwisp run`" + ` dispatches the run through its REST API and follows the
live log stream. With no daemon running, the task is executed in this CLI
process from runwisp.toml.

Use --daemon to require a running daemon (and fail fast if none is up), or
--standalone to require in-process execution (and refuse if a daemon owns the
data dir). Without either flag, the local mode is auto-detected.

With --json, the run's outcome is printed to stdout as a single JSON document
(run id, status, exit code, duration, failed) once it finishes; live log lines
are diverted to stderr so stdout stays machine-readable.`,
	Example: `  runwisp run backup
  runwisp run backup --json          # print the run outcome as JSON
  runwisp run deploy --standalone    # run in-process, no daemon needed
  runwisp run build --url https://ci.example.com --password "$RUNWISP_PASSWORD"`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		exitCode, err := runTaskCLI(os.Stdout, args[0], flags)
		if err != nil {
			return err
		}
		if exitCode != 0 {
			os.Exit(exitCode)
		}
		return nil
	},
}

// runTaskCLI is the run command's RunE body, factored out so it can be unit
// tested without going through cobra: validates the flag combination, runs the
// task, and on failure writes the --json error document to w (the run never
// produced its own document, so stdout stays a single valid JSON document even
// on failure) before propagating the error.
func runTaskCLI(w io.Writer, taskName string, f Flags) (int, error) {
	var exitCode int
	var err error
	if runFlags.Daemon && runFlags.Standalone {
		err = errors.New("--daemon and --standalone are mutually exclusive")
	} else {
		exitCode, err = runExec(taskName, f)
	}
	if err != nil {
		if runFlags.JSON {
			_ = writeJSON(w, newExecErrorJSONDoc(taskName, err))
		}
		return exitCode, err
	}
	return exitCode, nil
}

func init() {
	runCmd.Flags().BoolVar(&runFlags.Daemon, "daemon", false, "require a running daemon and dispatch through its API")
	runCmd.Flags().BoolVar(&runFlags.Standalone, "standalone", false, "require no daemon and run the task in-process")
	runCmd.Flags().StringVar(&runFlags.URL, "url", "", "trigger the task on a remote daemon at this base URL (env: RUNWISP_URL)")
	runCmd.Flags().StringVar(&runFlags.Password, "password", "", "remote daemon password for --url (env: RUNWISP_PASSWORD)")
	runCmd.Flags().BoolVar(&runFlags.Detach, "detach", false, "with --url, trigger and print the run ID without following the log stream")
	runCmd.Flags().BoolVar(&runFlags.JSON, "json", false, "print the run outcome as a JSON document to stdout (log lines go to stderr)")
}

func runExec(taskName string, f Flags) (int, error) {
	if remoteURL := cmp.Or(runFlags.URL, os.Getenv("RUNWISP_URL")); remoteURL != "" {
		if runFlags.Daemon || runFlags.Standalone {
			return 0, errors.New("--url cannot be combined with --daemon or --standalone")
		}
		password := cmp.Or(runFlags.Password, os.Getenv("RUNWISP_PASSWORD"))
		return runExecViaRemote(taskName, remoteURL, password, runFlags.Detach)
	}

	daemonUp := isDaemonRunning(f)

	if runFlags.Daemon && !daemonUp {
		return 0, fmt.Errorf("--daemon was set but no daemon is running on data dir %q", f.DataDir)
	}
	if runFlags.Standalone && daemonUp {
		return 0, fmt.Errorf("--standalone was set but a daemon is already running on data dir %q", f.DataDir)
	}

	if daemonUp {
		return runExecViaDaemon(taskName, f)
	}
	return runExecStandalone(taskName, f)
}

// isDaemonRunning reports whether a daemon currently owns this data dir.
// Two writers on one SQLite file would corrupt state, so the standalone
// path must defer to the daemon when one is alive.
func isDaemonRunning(f Flags) bool {
	pidPath := datadir.PidFilePath(f.DataDir)
	pid, err := datadir.ReadPidFile(f.DataDir)
	if err != nil {
		return false
	}
	return processAlive(pid, pidPath)
}

// ensureNoRunningDaemon returns an error when a live daemon already owns this
// data dir, so a second `runwisp daemon` refuses to start rather than clobber
// the shared PID file and open the same SQLite database. A missing, unreadable,
// or stale PID file leaves the caller free to start.
func ensureNoRunningDaemon(f Flags) error {
	pidPath := datadir.PidFilePath(f.DataDir)
	pid, err := datadir.ReadPidFile(f.DataDir)
	if err != nil {
		return nil
	}
	if pid == os.Getpid() {
		return nil
	}
	if processAlive(pid, pidPath) {
		return fmt.Errorf("a RunWisp daemon (pid %d) is already running for data dir %q; stop it first", pid, f.DataDir)
	}
	return nil
}

// runExecViaDaemon dispatches the run through the running daemon's REST API
// (via its local Unix socket) and follows its SSE log stream until the run
// reaches a terminal state.
func runExecViaDaemon(taskName string, f Flags) (int, error) {
	client := apiclient.NewUnix(localAPISocketPath(f))
	if err := client.HealthCheck(); err != nil {
		return 0, fmt.Errorf("daemon is not reachable at %s (%w) — %s", localAPISocketPath(f), err, daemonNotRunningHint)
	}

	run, err := client.TriggerRun(taskName, nil)
	if err != nil {
		if apiclient.IsHTTPStatus(err, http.StatusNotFound) {
			return 0, unknownTaskError(taskName, daemonTaskNames(client))
		}
		return 0, fmt.Errorf("trigger %q: %w", taskName, err)
	}

	slog.Info("Task triggered", "name", taskName, "run", run.ID)
	exitCode, final, err := followRun(client, taskName, run.ID, runLineOut())
	if err != nil {
		return exitCode, err
	}
	if runFlags.JSON {
		return exitCode, finishExecJSON(os.Stdout, client, taskName, run.ID, final)
	}
	return exitCode, nil
}

// finishExecJSON writes the runJSONDoc for the run to w. It is the shared
// --json tail for the daemon and remote follow paths. When followRun already
// fetched the terminal run (the normal case), it is reused directly — a fresh
// GetRun here that failed would return an error and mask the exit code already
// in hand (see runTaskCLI), turning a known success into a spurious failure.
// final is nil only on an interrupted follow that produced no terminal state,
// where a fetch is the only way to report an outcome at all.
func finishExecJSON(w io.Writer, client *apiclient.Client, taskName, runID string, final *model.Run) error {
	if final == nil {
		var err error
		final, err = client.GetRun(runID)
		if err != nil {
			return fmt.Errorf("fetch final run state: %w", err)
		}
	}
	return writeJSON(w, newExecJSONDoc(taskName, final))
}

// runExecViaRemote dispatches the run to a remote daemon over the network. It
// reuses a cached JWT when one is valid, falling back to a CHAP handshake, and
// (unless detached) follows the SSE log stream to propagate the exit code.
func runExecViaRemote(taskName, baseURL, password string, detach bool) (int, error) {
	client := apiclient.NewPinned(baseURL, password, certPinStore{})

	// Health is a public endpoint — probe it before auth so an unreachable
	// daemon reports as such rather than as a login failure. A pinned-cert
	// mismatch also surfaces here (it fails the TLS handshake), so translate it
	// into known-hosts-style guidance instead of a generic "unreachable".
	if err := client.HealthCheck(); err != nil {
		var mismatch *apiclient.CertPinMismatchError
		if errors.As(err, &mismatch) {
			return 0, certPinMismatchError(baseURL, mismatch)
		}
		return 0, remoteUnreachableError(baseURL, err)
	}

	// Optimistically reuse a cached session; an expired token surfaces as a
	// 401 on the trigger, which triggerRemote re-authenticates and retries.
	if cached := loadCachedToken(baseURL); cached != "" {
		client.SetToken(cached)
	}
	if !client.IsAuthenticated() {
		if err := authenticateRemote(client, baseURL, password); err != nil {
			return 0, err
		}
	}

	run, err := triggerRemote(client, taskName, baseURL, password)
	if err != nil {
		return 0, err
	}

	slog.Info("Task triggered", "name", taskName, "run", run.ID, "url", baseURL)

	if detach {
		if runFlags.JSON {
			return 0, writeJSON(os.Stdout, newExecJSONDoc(taskName, run))
		}
		fmt.Println(run.ID)
		return 0, nil
	}
	exitCode, final, err := followRun(client, taskName, run.ID, runLineOut())
	if err != nil {
		return exitCode, err
	}
	if runFlags.JSON {
		return exitCode, finishExecJSON(os.Stdout, client, taskName, run.ID, final)
	}
	return exitCode, nil
}

// authenticateRemote runs the CHAP handshake and caches the resulting session
// token. It maps auth failures to user-facing errors.
func authenticateRemote(client *apiclient.Client, baseURL, password string) error {
	if password == "" {
		return remoteAuthRequiredError(baseURL)
	}
	if err := client.Authenticate(); err != nil {
		switch {
		case errors.Is(err, apiclient.ErrUnauthorized):
			return remoteAuthFailedError(baseURL)
		case errors.Is(err, apiclient.ErrRateLimited):
			return remoteRateLimitedError(baseURL)
		default:
			return fmt.Errorf("authenticate with %s: %w", baseURL, err)
		}
	}
	storeCachedToken(baseURL, client.Token())
	return nil
}

// triggerRemote triggers the run, re-authenticating once if a cached token has
// expired (401), and maps the daemon's error codes to user-facing messages.
func triggerRemote(client *apiclient.Client, taskName, baseURL, password string) (*model.Run, error) {
	run, err := client.TriggerRun(taskName, nil)
	if errors.Is(err, apiclient.ErrUnauthorized) {
		if authErr := authenticateRemote(client, baseURL, password); authErr != nil {
			return nil, authErr
		}
		run, err = client.TriggerRun(taskName, nil)
	}
	if err != nil {
		switch {
		case apiclient.IsHTTPStatus(err, http.StatusNotFound):
			return nil, unknownTaskError(taskName, daemonTaskNames(client))
		case apiclient.IsHTTPStatus(err, http.StatusForbidden):
			return nil, remoteManualTriggerDisabledError(taskName)
		case errors.Is(err, apiclient.ErrUnauthorized):
			return nil, remoteAuthFailedError(baseURL)
		case errors.Is(err, apiclient.ErrRateLimited):
			return nil, remoteRateLimitedError(baseURL)
		default:
			return nil, fmt.Errorf("trigger %q: %w", taskName, err)
		}
	}
	return run, nil
}

// followMaxStalls bounds consecutive log-stream re-opens that deliver nothing —
// no line and no Done event. A just-triggered run is handed back to the caller
// before its row is durably persisted (persistence is async), so the first
// stream(s) can find no run yet and the server closes them empty. We retry
// until the run becomes streamable; the bound only guards against a run ID that
// never materializes at all. followStallBackoff paces those retries so the
// not-yet-persisted row has time to land without busy-looping.
const (
	followMaxStalls    = 50
	followStallBackoff = 100 * time.Millisecond
)

// followRun streams the run's logs to lineOut until it reaches a terminal state,
// returning the exit code and — when it fetched one — the terminal run so a
// caller can render --json without re-fetching. final may be nil only alongside
// a non-nil error (or an interrupt), never on a clean terminal outcome.
func followRun(client *apiclient.Client, taskName, runID string, lineOut io.Writer) (int, *model.Run, error) {
	ctx, cancel := newSignalCancelContext()
	defer cancel()

	// Line numbers are zero-indexed; the server reads from=0 as the default
	// tail window and clamps to anchor 0 on a fresh run, so we see every line.
	from := int64(0)
	stalls := 0

	for {
		ch, err := client.StreamLogLines(ctx, runID, apiclient.StreamLogOpts{FromLine: from})
		if err != nil {
			return 0, nil, fmt.Errorf("open log stream: %w", err)
		}

		exitCode, final, highest, done, err := streamRunLogs(ch, client, taskName, runID, from, lineOut)
		if done {
			return exitCode, final, err
		}
		if ctx.Err() != nil {
			break // interrupted (Ctrl+C) — stop reconnecting
		}

		if highest >= from {
			// Progress: the SSE transport blipped mid-run (seen under heavy
			// load) but delivered lines before closing. Re-open from the next
			// unseen line so the persisted tail is never silently dropped — the
			// run is terminal by now, so the server replays the rest off disk
			// and sends Done. Making progress resets the stall budget.
			from = highest + 1
			stalls = 0
			continue
		}

		// Stall: the stream closed without a line or a Done event. The run is
		// not streamable yet — it was just triggered and its row lags the
		// trigger response (persistence is async), so the server can't resolve
		// it and closes the stream empty. Bailing here would exit 0 having
		// silently swallowed the run's output, violating "nothing silently
		// fails"; instead back off and retry until the row lands. (An accepted,
		// pending, or running run keeps the stream open rather than closing it
		// empty, so a stall only ever means "not persisted yet".)
		stalls++
		if stalls > followMaxStalls {
			break
		}
		select {
		case <-ctx.Done():
			return exitCodeFromRunState(client, runID)
		case <-time.After(followStallBackoff):
		}
	}

	// Stream never delivered a Done event (interrupted, or the run never became
	// streamable); fall back to the persisted terminal state for the exit code.
	return exitCodeFromRunState(client, runID)
}

// exitCodeFromRunState fetches the run's persisted state and derives its exit
// code, used as followRun's fallback when the log stream ends without a Done.
// It returns the fetched run alongside the code so callers can reuse it.
func exitCodeFromRunState(client *apiclient.Client, runID string) (int, *model.Run, error) {
	final, err := client.GetRun(runID)
	if err != nil {
		return 0, nil, fmt.Errorf("fetch final run state: %w", err)
	}
	return exitCodeFromRun(final), final, nil
}

// terminalFetchAttempts and terminalFetchBackoff bound the re-read below: one
// second in total, far longer than a single row write needs.
const (
	terminalFetchAttempts = 20
	terminalFetchBackoff  = 50 * time.Millisecond
)

// fetchTerminalRun fetches the run behind an SSE Done event, re-reading while
// the row still reads non-terminal (or cannot be read at all).
//
// Done means the run has ended, so a non-terminal row is a write that has not
// become visible yet — never the truth. Trusting it would report status
// "running" with a nil end_reason, and exitCodeFromRun reads a nil end_reason
// as success: a run that failed with exit 7 would exit 0 and a script chained
// on `runwisp run` would sail past the failure. The daemon flushes persistence
// before publishing a terminal event, so this normally succeeds on the first
// read; it stays as a belt against any path that publishes without the barrier.
func fetchTerminalRun(client *apiclient.Client, taskName, runID string) (*model.Run, error) {
	var run *model.Run
	var err error
	for attempt := range terminalFetchAttempts {
		if attempt > 0 {
			time.Sleep(terminalFetchBackoff)
		}
		run, err = client.GetRun(runID)
		if err == nil && run.Status == model.PhaseEnded {
			return run, nil
		}
	}
	if err != nil {
		return nil, err
	}
	// Out of budget with a still-non-terminal row. Report what we have rather
	// than fail, but say so — a wrong exit code must not pass unremarked.
	slog.Warn("Run reported done but its state still reads non-terminal; exit code may be wrong",
		"task", taskName, "run", runID, "status", run.Status)
	return run, nil
}

// newSignalCancelContext returns a context cancelled either by its own cancel
// func or by an interrupt/SIGTERM, so a CLI run forwards Ctrl+C to the stream.
func newSignalCancelContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-sig:
			cancel()
		case <-ctx.Done():
		}
	}()

	return ctx, func() {
		signal.Stop(sig)
		cancel()
	}
}

// streamRunLogs prints each streamed log line to stdout/stderr until the stream reports the run is done or errors.
// Lines below `from` are skipped as already seen. done=true means terminal outcome reached.
// On Done it returns the fetched terminal run so callers can reuse it (e.g. for
// the --json document) without a second GetRun that could fail and mask the
// already-known exit code.
func streamRunLogs(ch <-chan apiclient.LogStreamMsg, client *apiclient.Client, taskName, runID string, from int64, lineOut io.Writer) (exitCode int, final *model.Run, highest int64, done bool, err error) {
	highest = from - 1
	for msg := range ch {
		switch msg.Kind {
		case apiclient.LogStreamMsgKindLine:
			highest = printStreamedLogLine(msg, from, highest, lineOut)
		case apiclient.LogStreamMsgKindDone:
			run, getErr := fetchTerminalRun(client, taskName, runID)
			if getErr != nil {
				return 0, nil, highest, true, fmt.Errorf("fetch final run state: %w", getErr)
			}
			return exitCodeFromRun(run), run, highest, true, nil
		case apiclient.LogStreamMsgKindErr:
			return 0, nil, highest, true, fmt.Errorf("log stream error: %w", msg.ErrValue)
		}
	}
	return 0, nil, highest, false, nil
}

// printStreamedLogLine prints one streamed line unless it was already seen
// (N < from), returning the running highest line number printed. Stdout-stream
// lines go to lineOut (stdout normally, stderr under --json so stdout stays a
// single JSON document); stderr-stream lines always go to os.Stderr.
func printStreamedLogLine(msg apiclient.LogStreamMsg, from, highest int64, lineOut io.Writer) int64 {
	if msg.Line.N < from {
		return highest // already printed on an earlier connection
	}
	if msg.Line.Stream == logutil.StreamStderr {
		fmt.Fprintln(os.Stderr, msg.Line.Text)
	} else {
		fmt.Fprintln(lineOut, msg.Line.Text)
	}
	if msg.Line.N > highest {
		return msg.Line.N
	}
	return highest
}

// daemonTaskNames fetches the daemon's task list for the unknown-task
// suggestion. Best-effort: a failed fetch just means no list in the error.
func daemonTaskNames(client *apiclient.Client) []string {
	tasks, err := client.ListTasks()
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(tasks))
	for _, t := range tasks {
		names = append(names, t.Name)
	}
	return names
}

func exitCodeFromRun(run *model.Run) int {
	if run == nil {
		return 0
	}
	if run.EndReason != nil && *run.EndReason != model.ReasonSuccess {
		return run.ExitCode
	}
	return 0
}

// runExecStandalone runs the task in this CLI process. The data dir must not
// already be owned by a daemon (isDaemonRunning's caller has confirmed that).
func runExecStandalone(taskName string, f Flags) (int, error) {
	cfg, err := config.Load(f.CfgFile)
	if err != nil {
		return 0, fmt.Errorf("failed to load %s: %w", f.CfgFile, err)
	}

	if err := config.Validate(cfg); err != nil {
		return 0, fmt.Errorf("invalid configuration: %w", err)
	}

	var target *model.Task
	names := make([]string, 0, len(cfg.Tasks))
	for i := range cfg.Tasks {
		names = append(names, cfg.Tasks[i].Name)
		if cfg.Tasks[i].Name == taskName {
			target = &cfg.Tasks[i]
		}
	}
	if target == nil {
		return 0, unknownTaskError(taskName, names)
	}

	eventBus := events.NewEventBus()
	// One-shot CLI run carries no daemon fingerprint; an empty one keeps its
	// managed-container labels distinct from any running daemon's, so the CLI run
	// never reclaims a live daemon's container for the same slot.
	exec := initExecutor(cfg, eventBus, f.LogDir(), "")

	taskManager := runtime.NewTaskManager(exec, eventBus, time.Now)
	defer taskManager.Shutdown()

	taskManager.UpsertTask(target)

	done := make(chan *events.RunEvent, 1)
	unsubLog := eventBus.Subscribe(events.EventLogLine, runLogLineHandler(taskName, runLineOut()))
	defer unsubLog()

	termHandler := runTerminalHandler(taskName, done)
	unsubComplete := eventBus.Subscribe(events.EventRunCompleted, termHandler)
	defer unsubComplete()
	unsubFailed := eventBus.Subscribe(events.EventRunFailed, termHandler)
	defer unsubFailed()

	run, err := taskManager.TriggerRun(taskName, model.TriggeredByAPI)
	if err != nil {
		return 0, fmt.Errorf("failed to trigger task %q: %w", taskName, err)
	}

	slog.Info("Task triggered", "name", taskName, "run", run.ID)

	result := <-done

	if runFlags.JSON {
		if err := writeJSON(os.Stdout, newExecJSONDoc(taskName, result.Run)); err != nil {
			return 0, err
		}
	}

	if result.Run.EndReason != nil && *result.Run.EndReason != model.ReasonSuccess {
		return result.Run.ExitCode, nil
	}

	if !runFlags.JSON {
		slog.Info("Task completed", "name", taskName, "status", result.Run.Status)
	}
	return 0, nil
}

func runLogLineHandler(taskName string, lineOut io.Writer) func(events.Event) {
	return func(e events.Event) {
		ll, ok := e.Data.(events.LogLineEvent)
		if !ok || ll.TaskName != taskName {
			return
		}
		switch ll.Stream {
		case logutil.StreamStderr:
			fmt.Fprintln(os.Stderr, ll.Text)
		default:
			fmt.Fprintln(lineOut, ll.Text)
		}
	}
}

func runTerminalHandler(taskName string, done chan<- *events.RunEvent) func(events.Event) {
	return func(e events.Event) {
		if re, ok := e.Data.(events.RunEvent); ok && re.Run != nil && re.Run.TaskName == taskName {
			done <- &re
		}
	}
}
