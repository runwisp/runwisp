// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/apps/runwisp/internal/autostart"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/update"
	"github.com/runwisp/runwisp/apps/runwisp/internal/version"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// What to do with a running daemon once the binary is upgraded.
const (
	restartNow   = "now"
	restartIdle  = "idle"
	restartLater = "later"
)

// idlePollInterval is how often `--restart=idle` re-checks for active runs.
const idlePollInterval = 2 * time.Second

// upgradeHTTP bounds every release request, the archive download included.
var upgradeHTTP = &http.Client{Timeout: 5 * time.Minute}

var (
	upgradeYes     bool
	upgradeRestart string
)

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Upgrade RunWisp to the latest release",
	Long: `Upgrade RunWisp to the latest stable release, the same way it was installed:

  Homebrew            brew upgrade runwisp
  npm, bun, pnpm,     the global install command of that package manager
  yarn (global)
  install script or   downloads the release archive from GitHub, verifies its
  tar.gz              SHA-256 checksum, and swaps the binary in place

Docker images, npx, and project-local npm installs are upgraded outside
RunWisp; upgrade tells you how.

The running daemon keeps the old version until it restarts. When one is
running, upgrade asks what to do:

  now     restart it right away (in-flight runs get shutdown_timeout to finish)
  idle    wait until no task runs are active, then restart (services don't
          count, they restart either way). Ctrl-C stops waiting.
  later   leave it; run 'runwisp restart' when it suits you

--restart answers that question up front. Without a terminal and without
--restart, the daemon is left running.`,
	Example: `  runwisp upgrade
  runwisp upgrade --yes --restart=idle`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runUpgrade(cmd, flags, upgradeYes, upgradeRestart)
	},
}

func init() {
	upgradeCmd.Flags().BoolVarP(&upgradeYes, "yes", "y", false, "skip the upgrade confirmation")
	upgradeCmd.Flags().StringVar(&upgradeRestart, "restart", "", "what to do with a running daemon: now, idle, or later")
}

func runUpgrade(cmd *cobra.Command, f Flags, yesFlag bool, restartFlag string) error {
	if restartFlag != "" && !slices.Contains([]string{restartNow, restartIdle, restartLater}, restartFlag) {
		return fmt.Errorf("--restart must be now, idle, or later, not %q", restartFlag)
	}
	current := version.Version
	if !isReleaseBuild(current) {
		return &userFacingError{
			title:   "this runwisp was built from source",
			details: "Pull the latest source and rebuild it.",
		}
	}
	exe, err := update.Executable()
	if err != nil {
		return err
	}
	argv, refusal := update.Method(update.InstallSource(), exe, os.Geteuid() == 0)
	if refusal != "" {
		return &userFacingError{title: "runwisp upgrade can't update this install", details: refusal}
	}

	ctx := cmd.Context()
	out := cmd.OutOrStdout()
	latest, err := update.LatestVersion(ctx, upgradeHTTP, update.ReleasesURL)
	if err != nil {
		return err
	}
	if !update.IsNewer(latest, current) {
		fmt.Fprintf(out, "RunWisp is up to date (%s).\n", vPrefixed(current))
		return nil
	}

	in := bufio.NewReader(os.Stdin)
	isTTY := term.IsTerminal(int(os.Stdin.Fd()))
	prompter := autostart.NewStdioPrompter(in, cmd.ErrOrStderr(), isTTY, yesFlag)
	yes, err := prompter.Confirm(fmt.Sprintf("Upgrade RunWisp %s → %s?", vPrefixed(current), latest), true)
	if errors.Is(err, autostart.ErrNeedsYes) {
		return &userFacingError{title: "upgrade needs confirmation", details: "Re-run with --yes to upgrade without a terminal."}
	}
	if err != nil {
		return err
	}
	if !yes {
		return &userFacingError{title: "aborted, nothing changed"}
	}

	if err := upgradeBinary(ctx, cmd, argv, latest, exe); err != nil {
		return err
	}

	if !isDaemonRunning(f) {
		if os.Geteuid() == 0 && os.Getenv("SUDO_USER") != "" {
			fmt.Fprintln(out, "If a daemon runs as another user, restart it as that user with 'runwisp restart'.")
		}
		return nil
	}
	choice, err := resolveRestartChoice(restartFlag, in, out, isTTY, latest)
	if err != nil {
		return err
	}
	return restartAfterUpgrade(cmd, f, choice, latest)
}

// isReleaseBuild reports whether this binary is a published release, the only
// kind `runwisp upgrade` replaces.
func isReleaseBuild(current string) bool {
	return version.IsOfficial() && update.IsRelease(current)
}

// upgradeBinary runs the package manager's upgrade (argv), or replaces exe
// with the verified release archive when RunWisp owns the binary.
func upgradeBinary(ctx context.Context, cmd *cobra.Command, argv []string, latest, exe string) error {
	out := cmd.OutOrStdout()
	if argv != nil {
		fmt.Fprintf(out, "Running %s\n", strings.Join(argv, " "))
		pm := exec.CommandContext(ctx, argv[0], argv[1:]...)
		pm.Stdin, pm.Stdout, pm.Stderr = os.Stdin, out, cmd.ErrOrStderr()
		if err := pm.Run(); err != nil {
			return &userFacingError{title: argv[0] + " failed", details: err.Error()}
		}
		return nil
	}

	fmt.Fprintf(out, "Downloading RunWisp %s...\n", latest)
	err := update.SelfReplace(ctx, upgradeHTTP, update.ReleasesURL, latest, runtime.GOOS, runtime.GOARCH, exe)
	if errors.Is(err, fs.ErrPermission) {
		return &userFacingError{
			title:   "can't write " + exe,
			details: "Re-run the upgrade with sudo, or as the user that owns the file.",
		}
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Upgraded %s to %s.\n", exe, latest)
	return nil
}

// resolveRestartChoice returns flag when set, asks on a terminal, and falls
// back to "later" otherwise so a script never restarts a daemon unasked.
func resolveRestartChoice(flag string, in io.Reader, out io.Writer, isTTY bool, latest string) (string, error) {
	if flag != "" {
		return flag, nil
	}
	if !isTTY {
		return restartLater, nil
	}
	fmt.Fprintf(out, "The daemon still runs the old version. Restart it onto %s now, when idle, or later? [N/i/l] ", latest)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "", "n", "now":
		return restartNow, nil
	case "i", "idle":
		return restartIdle, nil
	default:
		return restartLater, nil
	}
}

func restartAfterUpgrade(cmd *cobra.Command, f Flags, choice, latest string) error {
	out := cmd.OutOrStdout()
	client := apiclient.NewUnix(localAPISocketPath(f))
	switch choice {
	case restartLater:
		fmt.Fprintf(out, "Run 'runwisp restart' to switch the daemon to %s.\n", latest)
		return nil
	case restartIdle:
		if err := waitForIdle(cmd.Context(), client, out, idlePollInterval); err != nil {
			return err
		}
	}
	if err := restartDaemon(cmd, f, false); err != nil {
		return err
	}
	if info, err := client.GetDaemonInfo(cmd.Context()); err == nil && update.IsNewer(latest, info.Version) {
		fmt.Fprintf(out, "Warning: the daemon still reports %s. It may run a different runwisp binary; check 'runwisp service status'.\n", vPrefixed(info.Version))
	}
	return nil
}

// waitForIdle blocks until no task runs are pending or running, printing the
// runs it waits on whenever that set changes.
func waitForIdle(ctx context.Context, client *apiclient.Client, out io.Writer, interval time.Duration) error {
	var last string
	for {
		tasks, err := client.ListTasks(ctx)
		if err != nil {
			return err
		}
		runs, _, err := client.ListRuns(ctx, apiclient.RunsParams{Status: "pending,running", Limit: 500})
		if err != nil {
			return err
		}
		busy := activeTaskRuns(tasks, runs)
		if len(busy) == 0 {
			return nil
		}
		if names := strings.Join(busy, ", "); names != last {
			fmt.Fprintf(out, "Waiting for %s to finish (Ctrl-C to stop waiting)...\n", names)
			last = names
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

// activeTaskRuns names the tasks with active runs, ignoring services: they are
// always running and restart with the daemon either way.
func activeTaskRuns(tasks []model.TaskResponse, runs []model.Run) []string {
	services := map[string]bool{}
	for _, t := range tasks {
		if t.Kind.IsService() {
			services[t.Name] = true
		}
	}
	var busy []string
	for _, r := range runs {
		if !services[r.TaskName] && !slices.Contains(busy, r.TaskName) {
			busy = append(busy, r.TaskName)
		}
	}
	slices.Sort(busy)
	return busy
}

func vPrefixed(v string) string {
	return "v" + strings.TrimPrefix(v, "v")
}
