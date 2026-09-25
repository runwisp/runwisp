// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
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

// remoteFlags carries the --url/--password pair start, stop, and restart
// share for dispatching to a remote daemon instead of the local one.
type remoteFlags struct {
	URL      string
	Password string
}

// controlRemote backs --url/--password on start, stop, and restart. Only one
// command runs per process, so all three bind the same struct.
var controlRemote remoteFlags

// addRemoteFlags registers --url/--password on cmd, mirroring run's own flags
// of the same name so all four commands describe them identically.
func addRemoteFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&controlRemote.URL, "url", "", "act on a remote daemon at this base URL (env: RUNWISP_URL)")
	cmd.Flags().StringVar(&controlRemote.Password, "password", "", "remote daemon password for --url (env: RUNWISP_PASSWORD)")
}

// resolve applies the RUNWISP_URL/RUNWISP_PASSWORD environment fallback, the
// same precedence run --url uses.
func (rf remoteFlags) resolve() (url, password string) {
	return cmp.Or(rf.URL, os.Getenv("RUNWISP_URL")), cmp.Or(rf.Password, os.Getenv("RUNWISP_PASSWORD"))
}

// controlFunc dispatches one control action for a task/service name or run ID.
// Its shape matches method expressions like (*apiclient.Client).StopTask.
type controlFunc func(c *apiclient.Client, ctx context.Context, name string) error

// resolveTargets expands the CLI's positional args into the tasks/services and
// run IDs to act on. Every arg is matched against task names with path.Match,
// so a literal name is simply a pattern that only matches itself —
// model.TaskNamePattern forbids glob metacharacters in names, so the two never
// collide. A glob skips locked (manual_trigger=false) entries so `stop '*'`
// doesn't fail on them; naming one literally still reaches the server's 403.
// With allowRunIDs, a ULID that names no task is a run ID. An arg that
// matches nothing is an error, so a typo acts on nothing at all.
func resolveTargets(args []string, tasks []model.TaskResponse, allowRunIDs bool) ([]model.TaskResponse, []string, error) {
	var targets []model.TaskResponse
	var runIDs []string
	seen := map[string]bool{}
	for _, arg := range args {
		matches, err := matchTasks(arg, tasks)
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

// matchTasks returns the tasks arg names: exact for a literal, every
// manually-controllable match for a glob. A glob that matches nothing is an
// error; a literal that matches nothing returns none, for the caller to judge.
func matchTasks(arg string, tasks []model.TaskResponse) ([]model.TaskResponse, error) {
	glob := strings.ContainsAny(arg, "*?[")
	var out []model.TaskResponse
	for _, t := range tasks {
		ok, err := path.Match(arg, t.Name)
		if err != nil {
			return nil, fmt.Errorf("invalid pattern %q: %w", arg, err)
		}
		if ok && (!glob || t.ManuallyControllable()) {
			out = append(out, t)
		}
	}
	if glob && len(out) == 0 {
		return nil, fmt.Errorf("pattern %q matched no controllable task or service", arg)
	}
	return out, nil
}

// controlTargets resolves args (tasks, services, globs across both, and — when
// stopRun is set — run IDs) and applies act to each over the local daemon
// socket or a remote daemon (--url/RUNWISP_URL). Name errors fail before
// anything is dispatched; after that every target is attempted and each
// failure is reported in the joined error. verb/done are the present/past-
// tense words for messages ("stop"/"stopped").
func controlTargets(cmd *cobra.Command, f Flags, rf remoteFlags, args []string, verb, done string, act, stopRun controlFunc) error {
	ctx := cmd.Context()
	baseURL, password := rf.resolve()
	client, err := controlClient(ctx, f, baseURL, password)
	if err != nil {
		return err
	}

	var tasks []model.TaskResponse
	if err := withSessionRetry(ctx, client, baseURL, password, func() (err error) {
		tasks, err = client.ListTasks(ctx)
		return err
	}); err != nil {
		return fmt.Errorf("list tasks: %w", err)
	}
	targets, runIDs, err := resolveTargets(args, tasks, stopRun != nil)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	var errs []error
	for _, t := range targets {
		if err := act(client, ctx, t.Name); err != nil {
			errs = append(errs, controlError(err, baseURL, verb, t.Name, false))
			continue
		}
		label := "Task"
		if t.Kind.IsService() {
			label = "Service"
		}
		fmt.Fprintf(out, "%s %q %s.\n", label, t.Name, done)
	}
	for _, id := range runIDs {
		if err := stopRun(client, ctx, id); err != nil {
			errs = append(errs, controlError(err, baseURL, verb, id, true))
			continue
		}
		fmt.Fprintf(out, "Run %s %s.\n", id, done)
	}
	return errors.Join(errs...)
}

// controlClient connects to either the local daemon socket or, when baseURL is
// set, a remote daemon, with the same reachability checks run uses.
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

// controlError maps one target's dispatch error to a user-facing one.
func controlError(err error, baseURL, verb, target string, isRun bool) error {
	if authErr := remoteAuthError(err, baseURL); authErr != nil {
		return authErr
	}
	switch {
	case isRun && apiclient.IsHTTPStatus(err, http.StatusNotFound):
		return fmt.Errorf("no run with ID %s", target)
	case isRun && apiclient.IsHTTPStatus(err, http.StatusBadRequest):
		return fmt.Errorf("run %s is not running", target)
	case isRun:
		return fmt.Errorf("%s run %s: %w", verb, target, err)
	case apiclient.IsHTTPStatus(err, http.StatusForbidden):
		return fmt.Errorf("cannot %s %q: manual_trigger = false in runwisp.toml", verb, target)
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
