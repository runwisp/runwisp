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
	"path"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/autostart"
	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/spf13/cobra"
)

// remoteFlags carries the --url/--password pair shared by stop, restart, and
// start for dispatching to a remote daemon instead of the local one.
type remoteFlags struct {
	URL      string
	Password string
}

// addRemoteFlags registers --url/--password on cmd, mirroring run's own flags
// of the same name so all four commands describe them identically.
func addRemoteFlags(cmd *cobra.Command, rf *remoteFlags) {
	cmd.Flags().StringVar(&rf.URL, "url", "", "act on a remote daemon at this base URL (env: RUNWISP_URL)")
	cmd.Flags().StringVar(&rf.Password, "password", "", "remote daemon password for --url (env: RUNWISP_PASSWORD)")
}

// resolve applies the RUNWISP_URL/RUNWISP_PASSWORD environment fallback, the
// same precedence run --url uses.
func (rf remoteFlags) resolve() (url, password string) {
	return cmp.Or(rf.URL, os.Getenv("RUNWISP_URL")), cmp.Or(rf.Password, os.Getenv("RUNWISP_PASSWORD"))
}

// targetKind distinguishes a task/service target from a run ID target (stop
// only accepts the latter).
type targetKind int

const (
	targetTaskKind targetKind = iota
	targetRunKind
)

// resolvedTarget is one concrete thing a control command will act on.
type resolvedTarget struct {
	kind      targetKind
	name      string
	isService bool // meaningful only when kind == targetTaskKind
}

// resolveTargets expands the CLI's positional args into a deduplicated list of
// concrete targets: literal task/service names, shell-style globs
// (path.Match) matched against every manually-controllable task or service,
// and — when allowRunIDs — run ULIDs. model.TaskNamePattern forbids glob
// metacharacters in a task name, so a literal name and a pattern are never
// ambiguous. A glob silently skips locked (manual_trigger=false) entries so
// `stop '*'` doesn't fail on them; naming one literally still reaches the
// server's 403.
func resolveTargets(args []string, tasks []model.TaskResponse, allowRunIDs bool) ([]resolvedTarget, error) {
	byName := make(map[string]model.TaskResponse, len(tasks))
	for _, t := range tasks {
		byName[t.Name] = t
	}

	var out []resolvedTarget
	seen := make(map[targetKind]map[string]bool)
	seen[targetTaskKind] = map[string]bool{}
	seen[targetRunKind] = map[string]bool{}
	add := func(rt resolvedTarget) {
		if seen[rt.kind][rt.name] {
			return
		}
		seen[rt.kind][rt.name] = true
		out = append(out, rt)
	}

	for _, arg := range args {
		if strings.ContainsAny(arg, "*?[") {
			if err := resolveGlobTarget(arg, tasks, add); err != nil {
				return nil, err
			}
			continue
		}
		if t, ok := byName[arg]; ok {
			add(resolvedTarget{kind: targetTaskKind, name: t.Name, isService: t.Kind.IsService()})
			continue
		}
		if allowRunIDs {
			if _, err := ulid.ParseStrict(arg); err == nil {
				add(resolvedTarget{kind: targetRunKind, name: arg})
				continue
			}
		}
		add(resolvedTarget{kind: targetTaskKind, name: arg})
	}
	return out, nil
}

// resolveGlobTarget expands a shell-style glob against every manually-
// controllable task or service, adding each match through add. Returns an
// error for an invalid pattern or one that matches nothing.
func resolveGlobTarget(pattern string, tasks []model.TaskResponse, add func(resolvedTarget)) error {
	matched := 0
	for _, t := range tasks {
		if !t.ManuallyControllable() {
			continue
		}
		ok, err := path.Match(pattern, t.Name)
		if err != nil {
			return fmt.Errorf("invalid pattern %q: %w", pattern, err)
		}
		if ok {
			add(resolvedTarget{kind: targetTaskKind, name: t.Name, isService: t.Kind.IsService()})
			matched++
		}
	}
	if matched == 0 {
		return fmt.Errorf("pattern %q matched no controllable task or service", pattern)
	}
	return nil
}

// controlAction bundles what a verb needs from controlTargets: verb/done are
// the present/past-tense words used in messages ("stop"/"stopped"); taskAct
// dispatches a task or service target; runAct (stop only) dispatches a run ID
// target.
type controlAction struct {
	verb, done  string
	taskAct     func(ctx context.Context, client *apiclient.Client, name string) error
	runAct      func(ctx context.Context, client *apiclient.Client, runID string) error
	allowRunIDs bool
}

var stopControlAction = controlAction{
	verb: "stop", done: "stopped",
	taskAct: func(ctx context.Context, client *apiclient.Client, name string) error {
		return client.StopTask(ctx, name)
	},
	runAct: func(ctx context.Context, client *apiclient.Client, runID string) error {
		return client.StopRun(ctx, runID)
	},
	allowRunIDs: true,
}

var restartControlAction = controlAction{
	verb: "restart", done: "restarted",
	taskAct: func(ctx context.Context, client *apiclient.Client, name string) error {
		return client.RestartTask(ctx, name, "cli")
	},
}

var startControlAction = controlAction{
	verb: "start", done: "started",
	taskAct: func(ctx context.Context, client *apiclient.Client, name string) error {
		return client.StartTask(ctx, name, "cli")
	},
}

// controlTargets resolves args to one or more targets (tasks, services, globs
// across both, and — for stop — run IDs) and applies a's action to each, over
// either the local daemon socket or a remote daemon (--url/RUNWISP_URL). Every
// target is attempted; a single target's error is returned directly (matching
// the pre-multi-target UX), while several targets report each failure to
// stderr and roll up into one aggregate error.
func controlTargets(cmd *cobra.Command, f Flags, args []string, a controlAction, rf remoteFlags) error {
	ctx := cmd.Context()
	out := cmd.OutOrStdout()

	baseURL, password := rf.resolve()
	client, err := controlClient(ctx, f, baseURL, password)
	if err != nil {
		return err
	}

	tasks, err := listTasksFor(ctx, client, baseURL, password)
	if err != nil {
		return err
	}
	targets, err := resolveTargets(args, tasks, a.allowRunIDs)
	if err != nil {
		return err
	}

	failures := 0
	for _, t := range targets {
		var actErr error
		if t.kind == targetRunKind {
			actErr = a.runAct(ctx, client, t.name)
		} else {
			actErr = a.taskAct(ctx, client, t.name)
		}
		if actErr != nil {
			failures++
			msg := controlErrorMessage(ctx, client, a.verb, t, actErr, baseURL)
			if len(targets) == 1 {
				return msg
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", t.name, msg)
			continue
		}
		printControlSuccess(out, t, a.done)
	}
	if failures > 0 {
		return fmt.Errorf("%d of %d targets failed", failures, len(targets))
	}
	return nil
}

// controlClient connects to either the local daemon socket or, when baseURL is
// set, a remote daemon — mirroring the reachability checks controlTargets'
// predecessor and run --url both used.
func controlClient(ctx context.Context, f Flags, baseURL, password string) (*apiclient.Client, error) {
	if baseURL != "" {
		return connectRemote(ctx, baseURL, password)
	}
	if !isDaemonRunning(f) {
		return nil, fmt.Errorf("no daemon is running on data dir %q — %s", f.DataDir, daemonNotRunningHint)
	}
	client := apiclient.NewUnix(localAPISocketPath(f))
	if err := client.HealthCheck(ctx); err != nil {
		return nil, fmt.Errorf("daemon is not reachable at %s (%w) — %s", localAPISocketPath(f), err, daemonNotRunningHint)
	}
	return client, nil
}

// listTasksFor fetches the task list to resolve targets against, re-
// authenticating once on a stale remote session the same way triggerRemote does.
func listTasksFor(ctx context.Context, client *apiclient.Client, baseURL, password string) ([]model.TaskResponse, error) {
	tasks, err := client.ListTasks(ctx)
	if baseURL != "" && errors.Is(err, apiclient.ErrUnauthorized) {
		if authErr := authenticateRemote(ctx, client, baseURL, password); authErr != nil {
			return nil, authErr
		}
		tasks, err = client.ListTasks(ctx)
	}
	if err != nil {
		if baseURL != "" {
			switch {
			case errors.Is(err, apiclient.ErrUnauthorized):
				return nil, remoteAuthFailedError(baseURL)
			case errors.Is(err, apiclient.ErrRateLimited):
				return nil, remoteRateLimitedError(baseURL)
			}
		}
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	return tasks, nil
}

// printControlSuccess writes the one-line confirmation for a successfully
// controlled target.
func printControlSuccess(out io.Writer, t resolvedTarget, done string) {
	if t.kind == targetRunKind {
		fmt.Fprintf(out, "Run %s %s.\n", t.name, done)
		return
	}
	label := "Task"
	if t.isService {
		label = "Service"
	}
	fmt.Fprintf(out, "%s %q %s.\n", label, t.name, done)
}

// controlErrorMessage maps a per-target dispatch error to a user-facing one.
func controlErrorMessage(ctx context.Context, client *apiclient.Client, verb string, t resolvedTarget, err error, baseURL string) error {
	if baseURL != "" {
		switch {
		case errors.Is(err, apiclient.ErrUnauthorized):
			return remoteAuthFailedError(baseURL)
		case errors.Is(err, apiclient.ErrRateLimited):
			return remoteRateLimitedError(baseURL)
		}
	}
	if t.kind == targetRunKind {
		switch {
		case apiclient.IsHTTPStatus(err, http.StatusNotFound):
			return fmt.Errorf("no run with ID %s", t.name)
		case apiclient.IsHTTPStatus(err, http.StatusBadRequest):
			return fmt.Errorf("run %s is not running", t.name)
		default:
			return fmt.Errorf("%s run %s: %w", verb, t.name, err)
		}
	}
	switch {
	case apiclient.IsHTTPStatus(err, http.StatusNotFound):
		return unknownTaskError(t.name, daemonTaskNames(ctx, client))
	case apiclient.IsHTTPStatus(err, http.StatusForbidden):
		return fmt.Errorf("cannot %s %q: manual_trigger = false in runwisp.toml", verb, t.name)
	default:
		return fmt.Errorf("%s %q: %w", verb, t.name, err)
	}
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
	systemWide, err := resolveManagedScope(deps, local)
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

// stopWaitTimeout returns how long to wait for the daemon to exit after
// SIGTERM: the configured [daemon] shutdown_timeout plus headroom for
// process teardown, floored at 15s when the config is unreadable or the
// timeout is short.
func stopWaitTimeout(f Flags) time.Duration {
	const floor = 15 * time.Second
	cfg, err := config.Load(f.CfgFile)
	if err != nil {
		return floor
	}
	return max(cfg.Daemon.ShutdownTimeout+5*time.Second, floor)
}
