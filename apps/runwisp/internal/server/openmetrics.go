// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/runwisp/runwisp/internal/storage/sqlcdb"
	"github.com/runwisp/runwisp/internal/version"
)

// openMetricsContentType is the OpenMetrics 1.0 text exposition content type.
// Prometheus negotiates this via Accept; emitting it unconditionally is fine
// because Prometheus also parses it without explicit negotiation.
const openMetricsContentType = "application/openmetrics-text; version=1.0.0; charset=utf-8"

// handleOpenMetrics renders the daemon's run/task/process state as an
// OpenMetrics text payload. It is registered outside the protected router
// group so external scrapers can hit it without a JWT — operators bind to
// loopback or firewall the port to keep it private.
func (srv *Server) handleOpenMetrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", openMetricsContentType)
	w.WriteHeader(http.StatusOK)

	summary, err := srv.db.GetRunSummary()
	if err != nil || summary == nil {
		summary = &sqlcdb.RunSummary{}
	}
	daemonInfo := srv.stats.GetDaemonInfo()
	stats := srv.stats.GetSystemStats()
	uptime := time.Since(srv.stats.startTime).Seconds()

	writeHelpType(w, "runwisp_runs_total", "counter", "Total runs that reached a terminal status, partitioned by status.")
	writeSample(w, "runwisp_runs_total", []labelPair{{"status", "success"}}, float64(summary.Success))
	writeSample(w, "runwisp_runs_total", []labelPair{{"status", "failed"}}, float64(summary.Failed))

	if summary.LastFailure != nil {
		writeHelpType(w, "runwisp_runs_last_failure_timestamp_seconds", "gauge", "Unix timestamp of the most recent failed run.")
		writeSample(w, "runwisp_runs_last_failure_timestamp_seconds", nil, float64(summary.LastFailure.Unix()))
	}

	writeHelpType(w, "runwisp_task_active_runs", "gauge", "Currently active runs per task.")
	for _, task := range daemonInfo.Tasks {
		kind := string(task.Kind)
		if kind == "" {
			kind = "task"
		}
		count := srv.taskManager.GetActiveRunCount(task.Name)
		writeSample(w, "runwisp_task_active_runs", []labelPair{
			{"task", task.Name},
			{"kind", kind},
		}, float64(count))
	}

	writeHelpType(w, "runwisp_daemon_cpu_percent", "gauge", "Host CPU usage as seen by the daemon (0-100).")
	writeSample(w, "runwisp_daemon_cpu_percent", nil, stats.CPUUsage)

	writeHelpType(w, "runwisp_daemon_memory_used_bytes", "gauge", "Host memory used as seen by the daemon, in bytes.")
	writeSample(w, "runwisp_daemon_memory_used_bytes", nil, float64(stats.MemUsed))

	writeHelpType(w, "runwisp_daemon_memory_total_bytes", "gauge", "Total host memory as seen by the daemon, in bytes.")
	writeSample(w, "runwisp_daemon_memory_total_bytes", nil, float64(stats.MemTotal))

	writeHelpType(w, "runwisp_daemon_uptime_seconds", "gauge", "Seconds since the daemon started.")
	writeSample(w, "runwisp_daemon_uptime_seconds", nil, uptime)

	writeHelpType(w, "runwisp_build_info", "gauge", "Build information about the running daemon; always 1.")
	writeSample(w, "runwisp_build_info", []labelPair{{"version", version.Version}}, 1)

	_, _ = io.WriteString(w, "# EOF\n")
}

type labelPair struct {
	name, value string
}

func writeHelpType(w io.Writer, name, metricType, help string) {
	fmt.Fprintf(w, "# HELP %s %s\n", name, escapeHelp(help))
	fmt.Fprintf(w, "# TYPE %s %s\n", name, metricType)
}

func writeSample(w io.Writer, name string, labels []labelPair, value float64) {
	if len(labels) == 0 {
		fmt.Fprintf(w, "%s %s\n", name, formatFloat(value))
		return
	}
	var sb strings.Builder
	sb.WriteString(name)
	sb.WriteByte('{')
	for i, l := range labels {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(l.name)
		sb.WriteString(`="`)
		sb.WriteString(escapeLabelValue(l.value))
		sb.WriteByte('"')
	}
	sb.WriteByte('}')
	fmt.Fprintf(w, "%s %s\n", sb.String(), formatFloat(value))
}

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// escapeLabelValue escapes the three characters OpenMetrics requires for
// label values: backslash, double quote, and newline.
func escapeLabelValue(s string) string {
	if !strings.ContainsAny(s, "\\\"\n") {
		return s
	}
	var sb strings.Builder
	sb.Grow(len(s) + 4)
	for _, r := range s {
		switch r {
		case '\\':
			sb.WriteString(`\\`)
		case '"':
			sb.WriteString(`\"`)
		case '\n':
			sb.WriteString(`\n`)
		default:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// escapeHelp escapes backslash and newline in HELP text (double quotes are
// allowed unescaped in HELP per the OpenMetrics spec).
func escapeHelp(s string) string {
	if !strings.ContainsAny(s, "\\\n") {
		return s
	}
	var sb strings.Builder
	sb.Grow(len(s) + 2)
	for _, r := range s {
		switch r {
		case '\\':
			sb.WriteString(`\\`)
		case '\n':
			sb.WriteString(`\n`)
		default:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}
