// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path"
	"strings"
	"syscall"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/runwisp/runwisp/apps/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/apps/runwisp/internal/autostart"
	"github.com/runwisp/runwisp/apps/runwisp/internal/config"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/spf13/cobra"
)

// remoteFlags carries the --url/--password pair the control verbs (start,
// stop, restart, pause, resume) share for dispatching to a remote daemon
// instead of the local one.
type remoteFlags struct {
	URL      string
	Password string
	// Token is a unit's hook token (--token on start/stop/restart): it acts
	// through the hook routes instead of a password session.
	Token string
}

// controlRemote backs --url/--password on the control verbs. Only one command
// runs per process, so they all bind the same struct.
var controlRemote remoteFlags

// addRemoteFlags registers --url/--password on cmd, mirroring run's own flags
// of the same name so all four commands describe them identically.
func addRemoteFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&controlRemote.URL, "url", "", "act on a remote daemon at this base URL (env: RUNWISP_URL)")
	cmd.Flags().StringVar(&controlRemote.Password, "password", "", "remote daemon password for --url (env: RUNWISP_PASSWORD)")
}

// addTokenFlag registers --token on start/stop/restart, the verbs a hook
// token can perform.
func addTokenFlag(cmd *cobra.Command) {
	cmd.Flags().StringVar(&controlRemote.Token, "token", "", "act with one of the unit's hook_tokens instead of a password; needs --url (env: RUNWISP_HOOK_TOKEN)")
}

// controlAttach backs --attach on start/stop/restart; like controlRemote, it
// is shared because only one command runs per process.
var controlAttach bool

func addAttachFlag(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&controlAttach, "attach", false, "then follow the targets' logs, like 'runwisp logs -f'")
}

// errAttachNeedsTarget rejects --attach on a daemon-wide stop/restart.
var errAttachNeedsTarget = errors.New("--attach needs a target: it follows the logs of the tasks and services you name")

// resolve applies the RUNWISP_URL/RUNWISP_PASSWORD environment fallback, the
// same precedence run --url uses.
func (rf remoteFlags) resolve() (url, password string) {
	return cmp.Or(rf.URL, os.Getenv("RUNWISP_URL")), cmp.Or(rf.Password, os.Getenv("RUNWISP_PASSWORD"))
}

// hookToken applies the RUNWISP_HOOK_TOKEN environment fallback to --token.
func (rf remoteFlags) hookToken() string {
	return cmp.Or(rf.Token, os.Getenv("RUNWISP_HOOK_TOKEN"))
}

// controlFunc dispatches one control action for a task/service name or run ID.
// Its shape matches method expressions like (*apiclient.Client).StopTask.
type controlFunc func(c *apiclient.Client, ctx context.Context, name string) error

// targetFilter decides which tasks a glob may pick for a control verb, and
// names them for the "matched nothing" error.
type targetFilter struct {
	eligible func(model.TaskResponse) bool
	noun     string
}

// controlVerb is one control action: its present/past-tense words for
// messages ("stop"/"stopped"), how to dispatch it for a task or service name
// and, when it takes run IDs, for a run, and what a glob may match. replaces
// marks a verb that ends the targets' active runs to start new ones: --attach
// follows only the new ones. hook is the hook action that performs the verb
// with --token, empty when no hook route does.
type controlVerb struct {
	verb, done   string
	act, stopRun controlFunc
	filter       targetFilter
	replaces     bool
	hook         model.HookAction
}

var (
	startVerb = controlVerb{verb: "start", done: "started", filter: controllableTargets, hook: model.HookStart,
		act: func(c *apiclient.Client, ctx context.Context, name string) error {
			return c.StartTask(ctx, name, "cli")
		}}
	stopVerb = controlVerb{verb: "stop", done: "stopped", filter: controllableTargets, hook: model.HookStop,
		act: (*apiclient.Client).StopTask, stopRun: (*apiclient.Client).StopRun}
	restartVerb = controlVerb{verb: "restart", done: "restarted", filter: controllableTargets, replaces: true, hook: model.HookRestart,
		act: func(c *apiclient.Client, ctx context.Context, name string) error {
			return c.RestartTask(ctx, name, "cli")
		}}
	pauseVerb  = controlVerb{verb: "pause", done: "paused", filter: pausableTargets, act: (*apiclient.Client).PauseTask}
	resumeVerb = controlVerb{verb: "resume", done: "resumed", filter: pausedTargets, act: (*apiclient.Client).ResumeTask}
)

// controllableTargets is the start/stop/restart filter: anything not locked
// with manual_trigger = false.
var controllableTargets = targetFilter{
	eligible: func(t model.TaskResponse) bool { return t.ManuallyControllable() },
	noun:     "controllable task or service",
}

// allTargets is the read-only filter (logs): reading a log isn't control, so
// a glob matches locked entries too.
var allTargets = targetFilter{
	eligible: func(model.TaskResponse) bool { return true },
	noun:     "task or service",
}

// resolveTargets expands the CLI's positional args into the tasks/services and
// run IDs to act on. Every arg is matched against task names with path.Match,
// so a literal name is simply a pattern that only matches itself —
// model.TaskNamePattern forbids glob metacharacters in names, so the two never
// collide. A glob skips entries the filter rejects (e.g. locked
// manual_trigger=false ones) so `stop '*'` doesn't fail on them; naming one
// literally still reaches the server's 403/409.
// With allowRunIDs, a ULID that names no task is a run ID. An arg that
// matches nothing is an error, so a typo acts on nothing at all.
func resolveTargets(args []string, tasks []model.TaskResponse, allowRunIDs bool, filter targetFilter) ([]model.TaskResponse, []string, error) {
	var targets []model.TaskResponse
	var runIDs []string
	seen := map[string]bool{}
	for _, arg := range args {
		matches, err := matchTasks(arg, tasks, filter)
		if err != nil {
			return nil, nil, err
		}
		for _, t := range matches {
			if !seen[t.Name] {
				seen[t.Name] = true
				targets = append(targets, t)
			}
		}
		if len(matches) > 0 {
			continue
		}
		if _, err := ulid.ParseStrict(arg); !allowRunIDs || err != nil {
			return nil, nil, unknownTaskError(arg, responseNames(tasks))
		}
		if !seen[arg] {
			seen[arg] = true
			runIDs = append(runIDs, arg)
		}
	}
	return targets, runIDs, nil
}

// matchTasks returns the tasks arg names: exact for a literal, every match the
// filter accepts for a glob. A glob that matches nothing is an error; a
// literal that matches nothing returns none, for the caller to judge.
func matchTasks(arg string, tasks []model.TaskResponse, filter targetFilter) ([]model.TaskResponse, error) {
	glob := strings.ContainsAny(arg, "*?[")
	var out []model.TaskResponse
	for _, t := range tasks {
		ok, err := path.Match(arg, t.Name)
		if err != nil {
			return nil, fmt.Errorf("invalid pattern %q: %w", arg, err)
		}
		if ok && (!glob || filter.eligible(t)) {
			out = append(out, t)
		}
	}
	if glob && len(out) == 0 {
		return nil, fmt.Errorf("pattern %q matched no %s", arg, filter.noun)
	}
	return out, nil
}

// controlTargets resolves args (tasks, services, globs across both, and, when
// v takes them, run IDs) and applies v to each over the local daemon socket or
// a remote daemon (--url/RUNWISP_URL). Name errors fail before anything is
// dispatched; after that every target is attempted and each failure is
// reported in the joined error.
//
// With attach, it then follows the logs of the targets that succeeded, as
// `logs -f` does. The event stream is opened and the targets' active runs listed
// before anything is dispatched, so every run the action starts is followed
// from its first line, however quickly it ends.
func controlTargets(cmd *cobra.Command, f Flags, rf remoteFlags, args []string, v controlVerb, attach bool) error {
	ctx := cmd.Context()
	if token := rf.hookToken(); token != "" && v.hook != "" {
		return controlWithToken(ctx, cmd.OutOrStdout(), rf, token, args, v, attach)
	}
	if attach {
		var stop context.CancelFunc
		ctx, stop = signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer stop()
	}
	client, baseURL, tasks, err := connectAndListTasks(ctx, f, rf)
	if err != nil {
		return err
	}
	targets, runIDs, err := resolveTargets(args, tasks, v.stopRun != nil, v.filter)
	if err != nil {
		return err
	}

	var att *logAttach
	if attach {
		if att, err = openLogAttach(ctx, client, baseURL, tasks, targets, runIDs); err != nil {
			return err
		}
	}
	targets, runIDs, err = v.dispatch(ctx, cmd.OutOrStdout(), client, baseURL, targets, runIDs)
	if att == nil || len(targets)+len(runIDs) == 0 {
		return err
	}
	if err != nil {
		slog.Warn("Following only the targets that succeeded", "error", err)
	}
	return errors.Join(err, att.follow(ctx, cmd.OutOrStdout(), cmd.ErrOrStderr(), targets, runIDs, v.replaces))
}

// dispatch applies v to every target and run ID, confirming each on out. It
// returns the ones that succeeded and the joined failures.
func (v controlVerb) dispatch(ctx context.Context, out io.Writer, client *apiclient.Client, baseURL string, targets []model.TaskResponse, runIDs []string) ([]model.TaskResponse, []string, error) {
	var errs []error
	var okTargets []model.TaskResponse
	var okRuns []string
	for _, t := range targets {
		if err := v.act(client, ctx, t.Name); err != nil {
			errs = append(errs, controlError(err, baseURL, v.verb, t.Name, false))
			continue
		}
		label := "Task"
		if t.Kind.IsService() {
			label = "Service"
		}
		fmt.Fprintf(out, "%s %q %s.\n", label, t.Name, v.done)
		okTargets = append(okTargets, t)
	}
	for _, id := range runIDs {
		if err := v.stopRun(client, ctx, id); err != nil {
			errs = append(errs, controlError(err, baseURL, v.verb, id, true))
			continue
		}
		fmt.Fprintf(out, "Run %s %s.\n", id, v.done)
		okRuns = append(okRuns, id)
	}
	return okTargets, okRuns, errors.Join(errs...)
}

// connectAndListTasks connects to the local or remote daemon (see
// controlClient) and lists its tasks, logging in again if a cached remote
// session has expired. baseURL is empty for the local socket.
func connectAndListTasks(ctx context.Context, f Flags, rf remoteFlags) (*apiclient.Client, string, []model.TaskResponse, error) {
	baseURL, password := rf.resolve()
	client, err := controlClient(ctx, f, baseURL, password)
	if err != nil {
		return nil, "", nil, err
	}
	var tasks []model.TaskResponse
	if err := withSessionRetry(ctx, client, baseURL, password, func() (err error) {
		tasks, err = client.ListTasks(ctx)
		return err
	}); err != nil {
		return nil, "", nil, fmt.Errorf("list tasks: %w", err)
	}
	return client, baseURL, tasks, nil
}

// controlClient connects to either the local daemon socket or, when baseURL is
// set, a remote daemon, with the same reachability checks run uses.
func controlClient(ctx context.Context, f Flags, baseURL, password string) (*apiclient.Client, error) {
	if baseURL != "" {
		return connectRemote(ctx, baseURL, password)
	}
	if !isDaemonRunning(f) {
		return nil, fmt.Errorf("no daemon is running on data dir %q — %s", f.DataDir, controlNotRunningHint(f))
	}
	return connectLocal(ctx, f)
}

// controlNotRunningHint is daemonNotRunningHint, except inside a RunWisp run
// (which always has RUNWISP_INSTANCE_INDEX). A task that runs as another user
// can't open the daemon's socket, and sudo is the wrong advice there: a hook
// token is the way back in.
func controlNotRunningHint(f Flags) string {
	if os.Getenv("RUNWISP_INSTANCE_INDEX") == "" {
		return daemonNotRunningHint(f)
	}
	return "this runs inside a RunWisp task, which can't reach the daemon's socket when it runs as another user. " +
		"Give the unit hook_tokens in runwisp.toml and pass one with --url <daemon URL> --token <token> (or RUNWISP_URL and RUNWISP_HOOK_TOKEN)"
}

// controlWithToken applies v to each literal name through the hook routes,
// authenticated by a hook token instead of a session. A hook token can't list
// tasks or read logs, so globs, run IDs and --attach are refused up front.
func controlWithToken(ctx context.Context, out io.Writer, rf remoteFlags, token string, args []string, v controlVerb, attach bool) error {
	baseURL, _ := rf.resolve()
	switch {
	case baseURL == "":
		return errors.New("--token needs --url (or RUNWISP_URL): hook tokens work over the daemon's HTTP address, not its local socket")
	case attach:
		return errors.New("--attach can't be used with --token: a hook token can't read logs")
	}
	for _, arg := range args {
		if strings.ContainsAny(arg, "*?[") {
			return fmt.Errorf("--token takes literal task or service names, not the pattern %q: a hook token can't list tasks", arg)
		}
		if _, err := ulid.ParseStrict(arg); err == nil {
			return fmt.Errorf("--token takes task or service names, not the run ID %s", arg)
		}
	}
	client, err := dialRemote(ctx, baseURL, "")
	if err != nil {
		return err
	}
	client.SetToken(token)
	var errs []error
	for _, name := range args {
		if err := client.HookAction(ctx, v.hook, name); err != nil {
			errs = append(errs, hookControlError(err, baseURL, v.verb, name))
			continue
		}
		fmt.Fprintf(out, "%q %s.\n", name, v.done)
	}
	return errors.Join(errs...)
}

// hookControlError maps a hook route's rejections, which mean something
// different from a session's: a 401 is a token the unit doesn't have, a 403 a
// token whose allow list leaves the verb out.
func hookControlError(err error, baseURL, verb, name string) error {
	switch {
	case errors.Is(err, apiclient.ErrUnauthorized):
		return fmt.Errorf("%s rejected the hook token for %q: it must be one of that unit's hook_tokens in runwisp.toml", baseURL, name)
	case errors.Is(err, apiclient.ErrRateLimited):
		return fmt.Errorf("%s is refusing hook tokens from this IP after too many wrong ones: wait a minute and try again", baseURL)
	case apiclient.IsHTTPStatus(err, http.StatusForbidden):
		return fmt.Errorf("cannot %s %q: the hook token's allow list in runwisp.toml does not include %q", verb, name, verb)
	}
	return controlError(err, "", verb, name, false)
}

// connectLocal returns a client for the local daemon socket after a health
// check.
func connectLocal(ctx context.Context, f Flags) (*apiclient.Client, error) {
	client := apiclient.NewUnix(localAPISocketPath(f))
	if err := client.HealthCheck(ctx); err != nil {
		return nil, localUnreachableError(f, err)
	}
	return client, nil
}

func localUnreachableError(f Flags, err error) error {
	return fmt.Errorf("daemon is not reachable at %s (%w) — %s", localAPISocketPath(f), err, daemonNotRunningHint(f))
}

// controlError maps one target's dispatch error to a user-facing one.
func controlError(err error, baseURL, verb, target string, isRun bool) error {
	if authErr := remoteAuthError(err, baseURL); authErr != nil {
		return authErr
	}
	var statusErr *apiclient.HTTPStatusError
	switch {
	case isRun && apiclient.IsHTTPStatus(err, http.StatusNotFound):
		return fmt.Errorf("no run with ID %s", target)
	case isRun && apiclient.IsHTTPStatus(err, http.StatusBadRequest):
		return fmt.Errorf("run %s is not running", target)
	case isRun:
		return fmt.Errorf("%s run %s: %w", verb, target, err)
	case apiclient.IsHTTPStatus(err, http.StatusForbidden):
		return fmt.Errorf("cannot %s %q: manual_trigger = false in runwisp.toml", verb, target)
	case errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusConflict:
		return fmt.Errorf("cannot %s %q: %s", verb, target, statusErr.Detail())
	}
	return fmt.Errorf("%s %q: %w", verb, target, err)
}

// shouldDelegateStop reports whether `runwisp stop` should go through the
// init system instead of signalling the PID directly. Only a managed unit
// that is actually running is delegated — raw SIGTERM on a managed unit
// would leave the manager's view of the service out of sync.
func shouldDelegateStop(st autostart.Status) bool {
	return st.UnitExists && st.UnitManaged && st.Running
}

// shouldDelegateRestart reports whether `runwisp restart` should go through
// the init system. Unlike stop, a stopped-but-enabled unit also delegates:
// `systemctl restart` / `kickstart -k` starts it under the manager, whereas
// spawning our own detached process would leave two owners racing on the
// next boot.
func shouldDelegateRestart(st autostart.Status) bool {
	return st.UnitExists && st.UnitManaged && (st.Running || st.Autostart)
}

// serviceManagerName names the init system for user-facing messages.
func serviceManagerName(st autostart.Status) string {
	switch st.OS {
	case "linux":
		return "systemd"
	case "darwin":
		return "launchd"
	default:
		return "the service manager"
	}
}

// serviceState resolves the autostart installer and probes its Status.
// Any failure — unsupported OS, missing HOME, an ambiguous scope, status
// probe error — returns ok=false and the caller falls back to the direct
// PID/SIGTERM path. The service layer is best-effort sugar; it must never
// block a plain stop. local mirrors the --local flag; without it the scope
// is detected from what is actually installed.
func serviceState(cmd *cobra.Command, f Flags, local bool) (autostart.Installer, autostart.InstallOptions, autostart.Status, bool) {
	deps, err := autostart.DefaultDeps(cmd.OutOrStdout(), os.Stdin, true)
	if err != nil {
		return nil, autostart.InstallOptions{}, autostart.Status{}, false
	}
	// A non-root command against its own data dir means its own daemon, so it
	// looks only at the user unit, even when a system unit also exists.
	systemWide, err := resolveManagedScope(deps, local || !ownsSystemDaemon(f, os.Geteuid()))
	if err != nil {
		return nil, autostart.InstallOptions{}, autostart.Status{}, false
	}
	installer, err := autostart.New(deps)
	if err != nil {
		return nil, autostart.InstallOptions{}, autostart.Status{}, false
	}
	opts, err := resolveStatusOptions(f, systemWide)
	if err != nil {
		return nil, autostart.InstallOptions{}, autostart.Status{}, false
	}
	st, err := installer.Status(context.Background(), opts)
	if err != nil {
		return nil, autostart.InstallOptions{}, autostart.Status{}, false
	}
	return installer, opts, st, true
}

// ownsSystemDaemon reports whether a command could mean the system service's
// daemon. A non-root `runwisp restart` against its own ./.runwisp is talking to
// its own daemon, and must not restart the system one (through sudo) because a
// system unit happens to exist on the box.
func ownsSystemDaemon(f Flags, euid int) bool {
	return euid == 0 || f.DataDir == defaultDataDir(0, "")
}

// stopWaitTimeout returns how long to wait for the daemon to exit after
// SIGTERM: the daemon's own shutdownBudget for [daemon] shutdown_timeout (the
// default when the config is unreadable) plus headroom for process teardown,
// floored at 15s. `service install` bakes the same value into the unit as the
// service manager's stop timeout.
func stopWaitTimeout(cfgPath string) time.Duration {
	const teardownMargin = 2 * time.Second
	timeout := config.DefaultDaemonShutdown
	if cfg, err := config.Load(cfgPath); err == nil {
		timeout = cfg.Daemon.ShutdownTimeout
	}
	return max(shutdownBudget(timeout)+teardownMargin, 15*time.Second)
}
