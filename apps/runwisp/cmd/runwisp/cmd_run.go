// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"log/slog"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/datadir"
	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runtime"
	"github.com/runwisp/runwisp/internal/server"
	"github.com/spf13/cobra"
)

var runFlags struct {
	remoteFlags
	Daemon     bool
	Standalone bool
	Detach     bool
	JSON       bool
	Params     []string
}

// runLineOut is where a run's stdout-stream log lines are written. In --json
// mode they go to stderr so stdout carries only the final runJSONDoc; stderr-
// stream lines always go to os.Stderr regardless.
func runLineOut(asJSON bool) io.Writer {
	if asJSON {
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
  runwisp run build --url https://ci.example.com --password "$RUNWISP_PASSWORD"
  runwisp run backup --param source=/data --param dest=/mnt/backup`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		exitCode, err := runTaskCLI(cmd.Context(), os.Stdout, args[0], flags)
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
func runTaskCLI(ctx context.Context, w io.Writer, taskName string, f Flags) (int, error) {
	var exitCode int
	var err error
	if runFlags.Daemon && runFlags.Standalone {
		err = errors.New("--daemon and --standalone are mutually exclusive")
	} else {
		exitCode, err = runExec(ctx, taskName, f)
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
	runCmd.Flags().StringArrayVar(&runFlags.Params, "param", nil, "supply a task parameter as key=value (repeatable); unset parameters use their declared default")
}

// parseParamFlags turns repeated --param key=value flags into the
// map[string]*string that TriggerRun/TriggerRunOptions expect. A key not
// mentioned is simply absent from the map, which model.ResolveParamValues
// resolves to the task's declared default — the CLI has no syntax for the
// REST/UI "explicit omit" case (a present key mapped to nil), since that only
// matters for overriding a default from a pre-filled form.
func parseParamFlags(raw []string) (map[string]*string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	params := make(map[string]*string, len(raw))
	for _, kv := range raw {
		key, value, ok := strings.Cut(kv, "=")
		if !ok {
			return nil, fmt.Errorf("invalid --param %q: expected key=value", kv)
		}
		params[key] = &value
	}
	return params, nil
}

func runExec(ctx context.Context, taskName string, f Flags) (int, error) {
	params, err := parseParamFlags(runFlags.Params)
	if err != nil {
		return 0, err
	}

	if remoteURL, password := runFlags.resolve(); remoteURL != "" {
		if runFlags.Daemon || runFlags.Standalone {
			return 0, errors.New("--url cannot be combined with --daemon or --standalone")
		}
		return runExecViaRemote(ctx, taskName, remoteURL, password, runFlags.Detach, runFlags.JSON, params)
	}

	daemonUp := isDaemonRunning(f)

	if runFlags.Daemon && !daemonUp {
		return 0, fmt.Errorf("--daemon was set but no daemon is running on data dir %q", f.DataDir)
	}
	if runFlags.Standalone && daemonUp {
		return 0, fmt.Errorf("--standalone was set but a daemon is already running on data dir %q", f.DataDir)
	}

	if daemonUp {
		return runExecViaDaemon(ctx, taskName, f, params, runFlags.JSON)
	}
	return runExecStandalone(taskName, f, params, runFlags.JSON)
}

// isDaemonRunning reports whether a daemon currently owns this data dir.
// Two writers on one SQLite file would corrupt state, so the standalone
// path must defer to the daemon when one is alive. It checks the lock, not the
// PID, because a starting daemon holds the lock before it writes its PID.
func isDaemonRunning(f Flags) bool {
	return datadir.PidFileLocked(datadir.PidFilePath(f.DataDir))
}

// runExecViaDaemon dispatches the run through the running daemon's REST API
// (via its local Unix socket) and follows its SSE log stream until the run
// reaches a terminal state.
func runExecViaDaemon(ctx context.Context, taskName string, f Flags, params map[string]*string, asJSON bool) (int, error) {
	client, err := connectLocal(ctx, f)
	if err != nil {
		return 0, err
	}

	run, err := client.TriggerRun(ctx, taskName, params, "cli")
	if err != nil {
		if apiclient.IsHTTPStatus(err, http.StatusNotFound) {
			return 0, unknownTaskError(taskName, daemonTaskNames(ctx, client))
		}
		return 0, fmt.Errorf("trigger %q: %w", taskName, err)
	}

	slog.Info("Task triggered", "name", taskName, "run", run.ID)
	exitCode, final, err := followRun(ctx, client, taskName, run.ID, runLineOut(asJSON))
	if err != nil {
		return exitCode, err
	}
	if asJSON {
		return exitCode, finishExecJSON(ctx, os.Stdout, client, taskName, run.ID, final)
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
func finishExecJSON(ctx context.Context, w io.Writer, client *apiclient.Client, taskName, runID string, final *model.Run) error {
	if final == nil {
		var err error
		final, err = client.GetRun(ctx, runID)
		if err != nil {
			return fmt.Errorf("fetch final run state: %w", err)
		}
	}
	return writeJSON(w, newExecJSONDoc(taskName, final))
}

// dialRemote returns a pinned client for a remote daemon after a health check,
// surfacing a cert-pin mismatch or unreachability with the same guidance on
// every --url command. It does not authenticate.
func dialRemote(ctx context.Context, baseURL, password string) (*apiclient.Client, error) {
	client := apiclient.NewPinned(baseURL, password, certPinStore{})

	// Health is a public endpoint — probe it before auth so an unreachable
	// daemon reports as such rather than as a login failure. A pinned-cert
	// mismatch also surfaces here (it fails the TLS handshake), so translate it
	// into known-hosts-style guidance instead of a generic "unreachable".
	if err := client.HealthCheck(ctx); err != nil {
		var mismatch *apiclient.CertPinMismatchError
		if errors.As(err, &mismatch) {
			return nil, certPinMismatchError(baseURL, mismatch)
		}
		return nil, remoteUnreachableError(baseURL, err)
	}
	return client, nil
}

// connectRemote dials a remote daemon (see dialRemote), then reuses a cached
// session token or logs in via CHAP if the caller supplies a password. Shared
// by run --url and the stop/restart/start --url control commands.
func connectRemote(ctx context.Context, baseURL, password string) (*apiclient.Client, error) {
	client, err := dialRemote(ctx, baseURL, password)
	if err != nil {
		return nil, err
	}

	// Optimistically reuse a cached session; an expired token surfaces as a
	// 401 on the first real call, which the caller re-authenticates and retries.
	if cached := loadCachedToken(baseURL); cached != "" {
		client.SetToken(cached)
		return client, nil
	}
	if err := authenticateRemote(ctx, client, baseURL, password); err != nil {
		return nil, err
	}
	return client, nil
}

// runExecViaRemote dispatches the run to a remote daemon over the network. It
// reuses a cached JWT when one is valid, falling back to a CHAP handshake, and
// (unless detached) follows the SSE log stream to propagate the exit code.
func runExecViaRemote(ctx context.Context, taskName, baseURL, password string, detach, asJSON bool, params map[string]*string) (int, error) {
	client, err := connectRemote(ctx, baseURL, password)
	if err != nil {
		return 0, err
	}

	run, err := triggerRemote(ctx, client, taskName, baseURL, password, params)
	if err != nil {
		return 0, err
	}

	slog.Info("Task triggered", "name", taskName, "run", run.ID, "url", baseURL)

	if detach {
		if asJSON {
			return 0, writeJSON(os.Stdout, newExecJSONDoc(taskName, run))
		}
		fmt.Println(run.ID)
		return 0, nil
	}
	exitCode, final, err := followRun(ctx, client, taskName, run.ID, runLineOut(asJSON))
	if err != nil {
		return exitCode, err
	}
	if asJSON {
		return exitCode, finishExecJSON(ctx, os.Stdout, client, taskName, run.ID, final)
	}
	return exitCode, nil
}

// authenticateRemote runs the CHAP handshake and caches the resulting session
// token. It maps auth failures to user-facing errors.
func authenticateRemote(ctx context.Context, client *apiclient.Client, baseURL, password string) error {
	if password == "" {
		return remoteAuthRequiredError(baseURL)
	}
	if err := client.Authenticate(ctx); err != nil {
		return remoteLoginError(err, baseURL)
	}
	storeCachedToken(baseURL, client.Token())
	return nil
}

// remoteLoginError maps a failed CHAP login to its user-facing error.
func remoteLoginError(err error, baseURL string) error {
	if authErr := remoteAuthError(err, baseURL); authErr != nil {
		return authErr
	}
	return fmt.Errorf("authenticate with %s: %w", baseURL, err)
}

// withSessionRetry runs call, re-authenticating once and retrying if a cached
// remote session has expired (401), and maps a final 401/429 to its user-
// facing error. baseURL is empty for the local socket, which has no session.
func withSessionRetry(ctx context.Context, client *apiclient.Client, baseURL, password string, call func() error) error {
	err := call()
	if baseURL != "" && errors.Is(err, apiclient.ErrUnauthorized) {
		if authErr := authenticateRemote(ctx, client, baseURL, password); authErr != nil {
			return authErr
		}
		err = call()
	}
	if authErr := remoteAuthError(err, baseURL); authErr != nil {
		return authErr
	}
	return err
}

// triggerRemote triggers the run via withSessionRetry and maps the daemon's
// error codes to user-facing messages.
func triggerRemote(ctx context.Context, client *apiclient.Client, taskName, baseURL, password string, params map[string]*string) (*model.Run, error) {
	var run *model.Run
	err := withSessionRetry(ctx, client, baseURL, password, func() (err error) {
		run, err = client.TriggerRun(ctx, taskName, params, "cli")
		return err
	})
	if err != nil {
		switch {
		case apiclient.IsHTTPStatus(err, http.StatusNotFound):
			return nil, unknownTaskError(taskName, daemonTaskNames(ctx, client))
		case apiclient.IsHTTPStatus(err, http.StatusForbidden):
			return nil, remoteManualTriggerDisabledError(taskName)
		default:
			return nil, fmt.Errorf("trigger %q: %w", taskName, err)
		}
	}
	return run, nil
}

// followRun streams the run's logs to lineOut until it reaches a terminal state,
// returning the exit code and — when it fetched one — the terminal run so a
// caller can render --json without re-fetching. final may be nil only alongside
// a non-nil error (or an interrupt), never on a clean terminal outcome.
func followRun(ctx context.Context, client *apiclient.Client, taskName, runID string, lineOut io.Writer) (int, *model.Run, error) {
	// An interrupt/SIGTERM cancels ctx so Ctrl+C stops the stream.
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Line numbers are zero-indexed; from=0 replays every line of a fresh run.
	done, err := followRunLog(ctx, client, taskName, runID, 0, followQuietAfter, func(l server.LogLineEntry) {
		writeLogLine(lineOut, os.Stderr, l.Stream, l.Text, "")
	})
	if err != nil && !errors.Is(err, errLogStreamStalled) {
		return 0, nil, err
	}
	if done {
		run, getErr := fetchTerminalRun(context.Background(), client, taskName, runID)
		if getErr != nil {
			return 0, nil, fmt.Errorf("fetch final run state: %w", getErr)
		}
		return exitCodeFromRun(run), run, nil
	}
	// Stream never delivered a Done event (interrupted, or the run never became
	// streamable); fall back to the persisted terminal state for the exit code.
	// A fresh, uncancelled context: ctx is the interrupt-cancelled one, and the
	// whole point is to still report an accurate exit code after Ctrl+C.
	final, err := client.GetRun(context.Background(), runID)
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
func fetchTerminalRun(ctx context.Context, client *apiclient.Client, taskName, runID string) (*model.Run, error) {
	var run *model.Run
	var err error
	for attempt := range terminalFetchAttempts {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(terminalFetchBackoff):
			}
		}
		run, err = client.GetRun(ctx, runID)
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

// daemonTaskNames fetches the daemon's task list for the unknown-task
// suggestion. Best-effort: a failed fetch just means no list in the error.
func daemonTaskNames(ctx context.Context, client *apiclient.Client) []string {
	tasks, err := client.ListTasks(ctx)
	if err != nil {
		return nil
	}
	return responseNames(tasks)
}

func responseNames(tasks []model.TaskResponse) []string {
	names := make([]string, len(tasks))
	for i, t := range tasks {
		names[i] = t.Name
	}
	return names
}

// exitCodeFromRun maps a finished run to the CLI exit code: the process's own
// code when it failed, and 1 when RunWisp ended the run as unsuccessful even
// though the process exited 0 or never ran (a `failures` pattern match, a
// timeout or stop the task handled gracefully, a skipped or rejected run).
func exitCodeFromRun(run *model.Run) int {
	if run == nil || run.EndReason == nil || *run.EndReason == model.ReasonSuccess {
		return 0
	}
	if run.ExitCode <= 0 {
		return 1
	}
	return run.ExitCode
}

// runExecStandalone runs the task in this CLI process. The data dir must not
// already be owned by a daemon (isDaemonRunning's caller has confirmed that).
func runExecStandalone(taskName string, f Flags, params map[string]*string, asJSON bool) (int, error) {
	cfg, err := config.Load(f.CfgFile)
	if err != nil {
		return 0, fmt.Errorf("failed to load %s: %w", f.CfgFile, err)
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
	// Standalone talks to the run manager directly, bypassing the daemon/station
	// guard in internal/server.runService.TriggerRun — so it must enforce the
	// same rule itself (model.Task.CheckTrigger), or manual_trigger=false and
	// services stop being cron/API-triggerable-only everywhere as documented.
	switch target.CheckTrigger() {
	case model.TriggerBlockedService:
		return 0, fmt.Errorf("task %q: %w", taskName, server.ErrServiceNotRunnable)
	case model.TriggerBlockedManualDisabled:
		return 0, standaloneManualTriggerDisabledError(taskName)
	}

	eventBus := events.NewEventBus()
	// One-shot CLI run carries no daemon fingerprint; an empty one keeps its
	// managed-container labels distinct from any running daemon's, so the CLI run
	// never reclaims a live daemon's container for the same slot.
	exec := initExecutor(cfg, eventBus, f.LogDir(), "", nil)

	taskManager := runtime.NewTaskManager(exec, eventBus, time.Now)
	defer taskManager.Shutdown()

	taskManager.UpsertTask(target)

	done := make(chan *events.RunEvent, 1)
	unsubLog := eventBus.Subscribe(events.EventLogLine, runLogLineHandler(taskName, runLineOut(asJSON)))
	defer unsubLog()

	termHandler := runTerminalHandler(taskName, done)
	unsubComplete := eventBus.Subscribe(events.EventRunCompleted, termHandler)
	defer unsubComplete()
	unsubFailed := eventBus.Subscribe(events.EventRunFailed, termHandler)
	defer unsubFailed()

	run, err := taskManager.TriggerRunWithOptions(taskName, runtime.TriggerRunOptions{
		TriggeredBy: model.TriggeredByCLI,
		Params:      params,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to trigger task %q: %w", taskName, err)
	}

	slog.Info("Task triggered", "name", taskName, "run", run.ID)

	result := <-done

	if asJSON {
		if err := writeJSON(os.Stdout, newExecJSONDoc(taskName, result.Run)); err != nil {
			return 0, err
		}
	}

	if result.Run.EndReason != nil && *result.Run.EndReason != model.ReasonSuccess {
		return result.Run.ExitCode, nil
	}

	if !asJSON {
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
		writeLogLine(lineOut, os.Stderr, ll.Stream, ll.Text, "")
	}
}

func runTerminalHandler(taskName string, done chan<- *events.RunEvent) func(events.Event) {
	return func(e events.Event) {
		if re, ok := e.Data.(events.RunEvent); ok && re.Run != nil && re.Run.TaskName == taskName {
			done <- &re
		}
	}
}
