// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/testutil"
	"github.com/runwisp/runwisp/internal/version"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func buildOpenMetricsServer(t *testing.T, info *model.DaemonInfo) (*Server, *testutil.MockRunRepository, *mockTaskRunner) {
	t.Helper()
	repo := new(testutil.MockRunRepository)
	runner := new(mockTaskRunner)
	srv := &Server{
		db:          repo,
		taskManager: runner,
		stats:       newStatsProvider(info, time.Now().Add(-90*time.Second)),
	}
	return srv, repo, runner
}

func scrapeMetrics(t *testing.T, srv *Server) (int, string, http.Header) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	srv.handleOpenMetrics(rec, req)
	return rec.Code, rec.Body.String(), rec.Result().Header
}

// assertMetricFamily asserts each metric appears with a HELP+TYPE preamble
// and at least one sample line.
func assertMetricFamily(t *testing.T, body, name string) {
	t.Helper()
	assert.Contains(t, body, "# HELP "+name+" ", "missing HELP for %s", name)
	assert.Contains(t, body, "# TYPE "+name+" ", "missing TYPE for %s", name)
	hasSample := false
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, name+" ") || strings.HasPrefix(line, name+"{") {
			hasSample = true
			break
		}
	}
	assert.True(t, hasSample, "no sample line for %s", name)
}

func TestOpenMetrics_HappyPath(t *testing.T) {
	lastFailure := time.Date(2026, 4, 1, 12, 30, 0, 0, time.UTC)
	info := &model.DaemonInfo{
		Tasks: []model.TaskBrief{
			{Name: "nightly-backup", Kind: model.KindTask},
			{Name: "api-server", Kind: model.KindService},
		},
	}
	srv, repo, runner := buildOpenMetricsServer(t, info)
	repo.On("GetRunSummary", mock.Anything).Return(&model.RunSummary{
		Total:       45,
		Success:     42,
		Failed:      3,
		LastFailure: &lastFailure,
	}, nil)
	runner.On("GetActiveRunCount", "nightly-backup").Return(0)
	runner.On("GetActiveRunCount", "api-server").Return(1)

	code, body, header := scrapeMetrics(t, srv)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, openMetricsContentType, header.Get("Content-Type"))
	assert.True(t, strings.HasSuffix(body, "# EOF\n"), "body must end with # EOF terminator")

	for _, name := range []string{
		"runwisp_runs_total",
		"runwisp_runs_last_failure_timestamp_seconds",
		"runwisp_task_active_runs",
		"runwisp_daemon_cpu_percent",
		"runwisp_daemon_memory_used_bytes",
		"runwisp_daemon_memory_total_bytes",
		"runwisp_daemon_uptime_seconds",
		"runwisp_build_info",
	} {
		assertMetricFamily(t, body, name)
	}

	assert.Contains(t, body, `runwisp_runs_total{status="success"} 42`)
	assert.Contains(t, body, `runwisp_runs_total{status="failed"} 3`)
	assert.Contains(t, body, `runwisp_task_active_runs{task="nightly-backup",kind="task"} 0`)
	assert.Contains(t, body, `runwisp_task_active_runs{task="api-server",kind="service"} 1`)
	assert.Contains(t, body, `runwisp_build_info{version="`+version.Version+`"} 1`)

	repo.AssertExpectations(t)
	runner.AssertExpectations(t)
}

func TestOpenMetrics_OmitsLastFailureWhenNil(t *testing.T) {
	srv, repo, _ := buildOpenMetricsServer(t, &model.DaemonInfo{})
	repo.On("GetRunSummary", mock.Anything).Return(&model.RunSummary{
		Total:   5,
		Success: 5,
	}, nil)

	code, body, _ := scrapeMetrics(t, srv)
	require.Equal(t, http.StatusOK, code)
	assert.NotContains(t, body, "runwisp_runs_last_failure_timestamp_seconds",
		"nil LastFailure must not emit a zero — that would misleadingly read as 1970")
}

func TestOpenMetrics_EmptyState(t *testing.T) {
	srv, repo, _ := buildOpenMetricsServer(t, &model.DaemonInfo{})
	repo.On("GetRunSummary", mock.Anything).Return(&model.RunSummary{}, nil)

	code, body, header := scrapeMetrics(t, srv)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, openMetricsContentType, header.Get("Content-Type"))
	assert.True(t, strings.HasSuffix(body, "# EOF\n"))
	assert.Contains(t, body, `runwisp_runs_total{status="success"} 0`)
	assert.Contains(t, body, `runwisp_runs_total{status="failed"} 0`)
	// No tasks → metric family still announced via HELP/TYPE for client tooling.
	assert.Contains(t, body, "# TYPE runwisp_task_active_runs gauge")
}

func TestOpenMetrics_ToleratesSummaryError(t *testing.T) {
	srv, repo, _ := buildOpenMetricsServer(t, &model.DaemonInfo{})
	repo.On("GetRunSummary", mock.Anything).Return(nil, assert.AnError)

	code, body, _ := scrapeMetrics(t, srv)
	require.Equal(t, http.StatusOK, code,
		"scrape must succeed even if the summary query fails — partial data beats no data")
	assert.Contains(t, body, `runwisp_runs_total{status="success"} 0`)
	assert.True(t, strings.HasSuffix(body, "# EOF\n"))
}

func TestEscapeLabelValue(t *testing.T) {
	cases := map[string]string{
		"plain":            "plain",
		`with "quote"`:     `with \"quote\"`,
		`back\slash`:       `back\\slash`,
		"line1\nline2":     `line1\nline2`,
		`\\"all"\n`:        `\\\\\"all\"\\n`,
		"no escape needed": "no escape needed",
	}
	for in, want := range cases {
		assert.Equalf(t, want, escapeLabelValue(in), "input=%q", in)
	}
}

func TestEscapeHelp(t *testing.T) {
	// HELP text allows unescaped double quotes per the OpenMetrics spec, so the
	// only characters that need escaping are backslash and newline.
	cases := map[string]string{
		"plain":          "plain",
		`with "quote"`:   `with "quote"`,
		`back\slash`:     `back\\slash`,
		"line1\nline2":   `line1\nline2`,
		"\\\n":           `\\\n`,
		"":               "",
		"no escape here": "no escape here",
	}
	for in, want := range cases {
		assert.Equalf(t, want, escapeHelp(in), "input=%q", in)
	}
}

func TestOpenMetrics_LabelValueEscaping(t *testing.T) {
	// Real task names can't contain quotes/backslashes, but the encoder still
	// has to be correct — exercise it via a synthetic DaemonInfo entry so a
	// future loosening of the name rule doesn't silently produce broken output.
	tricky := "weird\"task\\name"
	info := &model.DaemonInfo{
		Tasks: []model.TaskBrief{{Name: tricky, Kind: model.KindTask}},
	}
	srv, repo, runner := buildOpenMetricsServer(t, info)
	repo.On("GetRunSummary", mock.Anything).Return(&model.RunSummary{}, nil)
	runner.On("GetActiveRunCount", tricky).Return(7)

	code, body, _ := scrapeMetrics(t, srv)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `runwisp_task_active_runs{task="weird\"task\\name",kind="task"} 7`)
}

func TestOpenMetrics_UptimeIsPositive(t *testing.T) {
	srv, repo, _ := buildOpenMetricsServer(t, &model.DaemonInfo{})
	repo.On("GetRunSummary", mock.Anything).Return(&model.RunSummary{}, nil)

	_, body, _ := scrapeMetrics(t, srv)
	uptimeLine := ""
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "runwisp_daemon_uptime_seconds ") {
			uptimeLine = line
			break
		}
	}
	require.NotEmpty(t, uptimeLine, "uptime sample line not found")
	// startTime is 90s in the past — sample value must reflect that, not zero
	// (a regression in newStatsProvider would surface here).
	assert.NotEqual(t, "runwisp_daemon_uptime_seconds 0", uptimeLine)
}
