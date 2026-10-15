// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/version"
	"github.com/spf13/cobra"
)

func TestResolveRestartChoice(t *testing.T) {
	cases := []struct {
		name, flag, input string
		isTTY             bool
		want              string
	}{
		{"flag wins over terminal", restartIdle, "later\n", true, restartIdle},
		{"no terminal never restarts", "", "", false, restartLater},
		{"enter means now", "", "\n", true, restartNow},
		{"i means idle", "", "i\n", true, restartIdle},
		{"l means later", "", "l\n", true, restartLater},
		{"closed stdin means now", "", "", true, restartNow},
	}
	for _, c := range cases {
		got, err := resolveRestartChoice(c.flag, strings.NewReader(c.input), io.Discard, c.isTTY, "v1.6.0")
		if err != nil || got != c.want {
			t.Errorf("%s: got %q, %v; want %q", c.name, got, err, c.want)
		}
	}
}

func TestActiveTaskRunsIgnoresServices(t *testing.T) {
	tasks := []model.TaskResponse{
		{Task: model.Task{Name: "backup", Kind: model.KindTask}},
		{Task: model.Task{Name: "web", Kind: model.KindService}},
	}
	runs := []model.Run{{TaskName: "web"}, {TaskName: "backup"}, {TaskName: "backup"}, {TaskName: "adhoc"}}
	if got := activeTaskRuns(tasks, runs); !slices.Equal(got, []string{"adhoc", "backup"}) {
		t.Fatalf("activeTaskRuns = %v, want [adhoc backup]", got)
	}
	if got := activeTaskRuns(tasks, []model.Run{{TaskName: "web"}}); len(got) != 0 {
		t.Fatalf("only a service running: got %v, want idle", got)
	}
}

func idleDaemon(t *testing.T, runs func() []model.Run) *apiclient.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tasks":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []model.TaskResponse{{Task: model.Task{Name: "web", Kind: model.KindService}}}})
		case "/api/runs":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": runs(), "total": len(runs())})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return apiclient.New(srv.URL, "")
}

func TestWaitForIdleReturnsOnceTasksFinish(t *testing.T) {
	var polls atomic.Int32
	client := idleDaemon(t, func() []model.Run {
		if polls.Add(1) < 3 {
			return []model.Run{{TaskName: "backup"}, {TaskName: "web"}}
		}
		return []model.Run{{TaskName: "web"}}
	})
	var out bytes.Buffer
	if err := waitForIdle(context.Background(), client, &out, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(out.String(), "Waiting for backup to finish"); got != 1 {
		t.Fatalf("want the wait notice printed once, got %d in %q", got, out.String())
	}
}

func TestWaitForIdleStopsOnCancel(t *testing.T) {
	client := idleDaemon(t, func() []model.Run { return []model.Run{{TaskName: "backup"}} })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitForIdle(ctx, client, io.Discard, time.Hour); err == nil {
		t.Fatal("a cancelled context should stop the wait")
	}
}

func TestWaitForIdleSurfacesDaemonErrors(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	if err := waitForIdle(context.Background(), apiclient.New(srv.URL, ""), io.Discard, time.Millisecond); err == nil {
		t.Fatal("an unreachable task list should fail the wait")
	}
}

func TestUpgradeBinaryRunsPackageManager(t *testing.T) {
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	if err := upgradeBinary(context.Background(), cmd, []string{"true"}, "v1.6.0", ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Running true") {
		t.Fatalf("output %q should name the command", out.String())
	}
	err := upgradeBinary(context.Background(), cmd, []string{"false"}, "v1.6.0", "")
	var ufe *userFacingError
	if !errors.As(err, &ufe) || ufe.title != "false failed" {
		t.Fatalf("failing package manager: got %v", err)
	}
}

func TestRestartAfterUpgradeLaterOnlyPrintsHint(t *testing.T) {
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := restartAfterUpgrade(cmd, Flags{DataDir: t.TempDir()}, restartLater, "v1.6.0"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "runwisp restart") {
		t.Fatalf("output %q should point at 'runwisp restart'", out.String())
	}
}

func TestRunUpgradeRejectsBadInput(t *testing.T) {
	cmd := &cobra.Command{}
	if err := runUpgrade(cmd, Flags{}, true, "sometime"); err == nil || !strings.Contains(err.Error(), "--restart") {
		t.Fatalf("bad --restart: got %v", err)
	}
	var ufe *userFacingError
	if err := runUpgrade(cmd, Flags{}, true, ""); !errors.As(err, &ufe) || !strings.Contains(ufe.title, "built from source") {
		t.Fatalf("dev build: got %v", err)
	}
}

func TestVPrefixed(t *testing.T) {
	if vPrefixed("1.6.0") != "v1.6.0" || vPrefixed("v1.6.0") != "v1.6.0" {
		t.Fatal("vPrefixed should add the v exactly once")
	}
}

type latestRT string

func (l latestRT) RoundTrip(*http.Request) (*http.Response, error) {
	h := http.Header{"Location": {"https://github.com/runwisp/runwisp/releases/tag/" + string(l)}}
	return &http.Response{StatusCode: http.StatusFound, Status: "302 Found", Header: h, Body: http.NoBody}, nil
}

// upgradeScenario fakes a release build whose newest release is latest,
// installed via source, with a `brew` on PATH that records its arguments.
func upgradeCmdWithCtx() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	return cmd
}

func upgradeScenario(t *testing.T, source, latest string) (brewLog string) {
	t.Helper()
	old, oldOfficial, oldHTTP := version.Version, version.Official, upgradeHTTP
	version.Version, version.Official = "1.5.0", "1"
	upgradeHTTP = &http.Client{Transport: latestRT(latest)}
	t.Cleanup(func() { version.Version, version.Official, upgradeHTTP = old, oldOfficial, oldHTTP })

	bin := t.TempDir()
	brewLog = filepath.Join(bin, "brew.log")
	script := "#!/bin/sh\necho \"$@\" >> " + brewLog + "\n"
	if err := os.WriteFile(filepath.Join(bin, "brew"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("RUNWISP_INSTALL_SOURCE", source)
	return brewLog
}

func TestRunUpgradeRefusesDockerInstall(t *testing.T) {
	upgradeScenario(t, "docker", "v1.6.0")
	var ufe *userFacingError
	if err := runUpgrade(upgradeCmdWithCtx(), Flags{}, true, ""); !errors.As(err, &ufe) || !strings.Contains(ufe.details, "docker compose pull") {
		t.Fatalf("docker install: got %v", err)
	}
}

func TestRunUpgradeUpToDateChangesNothing(t *testing.T) {
	brewLog := upgradeScenario(t, "homebrew", "v1.5.0")
	cmd := upgradeCmdWithCtx()
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := runUpgrade(cmd, Flags{}, true, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "up to date (v1.5.0)") {
		t.Fatalf("output %q should say up to date", out.String())
	}
	if _, err := os.Stat(brewLog); err == nil {
		t.Fatal("brew ran although nothing was newer")
	}
}

func TestRunUpgradeNeedsYesWithoutTerminal(t *testing.T) {
	upgradeScenario(t, "homebrew", "v1.6.0")
	cmd := upgradeCmdWithCtx()
	cmd.SetErr(io.Discard)
	var ufe *userFacingError
	if err := runUpgrade(cmd, Flags{}, false, ""); !errors.As(err, &ufe) || ufe.title != "upgrade needs confirmation" {
		t.Fatalf("no --yes, no terminal: got %v", err)
	}
}

func TestRunUpgradeHomebrewWithNoDaemon(t *testing.T) {
	brewLog := upgradeScenario(t, "homebrew", "v1.6.0")
	cmd := upgradeCmdWithCtx()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	if err := runUpgrade(cmd, Flags{DataDir: t.TempDir()}, true, ""); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(brewLog); strings.TrimSpace(string(b)) != "upgrade runwisp" {
		t.Fatalf("brew was run with %q", b)
	}
}
