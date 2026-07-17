// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// updateGolden rewrites the testdata/*.golden.json fixtures from the current
// output instead of comparing against them. Run with:
//
//	go test ./cmd/runwisp -run Golden -update
//
// then eyeball the diff before committing — the golden files are the schema
// contract the --json flags promise to agents.
var updateGolden = flag.Bool("update", false, "rewrite the testdata/*.golden.json fixtures")

// checkGolden compares got against the fixture at path, or rewrites it under
// -update. got is normalized through the same indented encoder the commands
// use so a field-order change surfaces as a diff.
func checkGolden(t *testing.T, path string, got []byte) {
	t.Helper()
	if *updateGolden {
		require.NoError(t, os.WriteFile(path, got, 0o600))
		return
	}
	want, err := os.ReadFile(path)
	require.NoErrorf(t, err, "read golden %s (run with -update to create it)", path)
	assert.Equalf(t, string(want), string(got), "golden mismatch for %s — run with -update to refresh", filepath.Base(path))
}

func TestValidateJSONGolden(t *testing.T) {
	f := writeValidateConfig(t, `
[scheduler]
timezone = "UTC"

[tasks.backup]
cron = "0 3 * * *"
run = "true"

[services.web]
run = "exec /usr/bin/web"
`)
	var buf bytes.Buffer
	require.NoError(t, runValidate(&buf, f, true))
	// The config path is an absolute temp dir that changes every run; pin it to
	// a stable basename so the golden stays deterministic.
	got := bytes.ReplaceAll(buf.Bytes(), []byte(f.CfgFile), []byte("runwisp.toml"))
	checkGolden(t, "testdata/validate.golden.json", got)
}

func TestListJSONGolden(t *testing.T) {
	f := Flags{CfgFile: writeConfig(t, `
[tasks.backup]
cron = "0 3 * * *"
run = "true"
description = "nightly backup"

[services.web]
run = "exec /usr/bin/web"
instances = 2
api_trigger = true
`)}
	var buf bytes.Buffer
	require.NoError(t, runList(&buf, f, true))
	checkGolden(t, "testdata/list.golden.json", buf.Bytes())
}

func TestStatusJSONGolden(t *testing.T) {
	start := time.Date(2026, 7, 15, 3, 0, 0, 0, time.UTC)
	end := start.Add(42 * time.Second)
	failed := model.ReasonFailed
	nextRun := "2026-07-16T03:00:00Z"

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/api/info", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(model.DaemonInfo{
			Version: "0.12.0", Port: 9477, SchedulingActive: true,
			ResolvedTimezone: "UTC", TimezoneSource: "config",
		})
	})
	mux.HandleFunc("/api/system", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(model.SystemStats{
			Version: "0.12.0", Uptime: "1h2m3s", CPUCores: 8,
			Host: "test-host", OS: "linux", Arch: "amd64", Name: "runwisp", WorkDir: "/srv",
		})
	})
	mux.HandleFunc("/api/tasks", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]model.TaskResponse{
			{Task: model.Task{Name: "backup", Cron: "0 3 * * *"}, NextRunAt: &nextRun},
			{Task: model.Task{Name: "web", Kind: model.KindService, APITrigger: true}},
		})
	})
	mux.HandleFunc("/api/tasks/backup/runs", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(server.RunsResponseBody{Total: 1, Runs: []model.Run{{
			ID: "01JZZBACKUP0000000000000000", TaskName: "backup", Status: model.PhaseEnded,
			EndReason: &failed, ExitCode: 1, TriggeredBy: model.TriggeredByCron,
			StartAt: &start, EndAt: &end,
		}}})
	})
	mux.HandleFunc("/api/tasks/web/runs", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(server.RunsResponseBody{Total: 1, Runs: []model.Run{{
			ID: "01JZZWEB00000000000000000000", TaskName: "web", Status: model.PhaseRunning,
			TriggeredBy: model.TriggeredByService, StartAt: &start,
		}}})
	})
	f := serveStatusSocket(t, mux)

	var buf bytes.Buffer
	require.NoError(t, runStatus(&buf, f, true))
	checkGolden(t, "testdata/status.golden.json", buf.Bytes())
}
