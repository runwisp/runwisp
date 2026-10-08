// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/runwisp/runwisp/internal/model"
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
func (srv *Server) handleOpenMetrics(w http.ResponseWriter, r *http.Request) {
	// Query before the first byte: once the 200 header is out, a DB failure can
	// only be reported as zeroed counters, which a scraper reads as a counter
	// reset rather than a failed scrape.
	summary, err := srv.db.GetRunSummary(r.Context())
	if err != nil {
		slog.Error("metrics: run summary query failed", "err", err)
		http.Error(w, "run summary unavailable", http.StatusInternalServerError)
		return
	}
	if summary == nil {
		summary = &model.RunSummary{}
	}
	w.Header().Set("Content-Type", openMetricsContentType)
	w.WriteHeader(http.StatusOK)

	// Live registry, not the boot-time DaemonInfo list: a reload adds and
	// removes tasks (see humaGetInfo).
	tasks := srv.currentTasks()
	if tasks == nil {
		tasks = srv.stats.GetDaemonInfo().Tasks
	}
	stats := srv.stats.GetSystemStats()
	uptime := time.Since(srv.stats.startTime).Seconds()

	writeHelpType(w, "runwisp_runs_total", "counter", "Total runs that reached a terminal status, partitioned by status.")
	writeSample(w, "runwisp_runs_total", []labelPair{{"status", "success"}}, float64(summary.Success))
	writeSample(w, "runwisp_runs_total", []labelPair{{"status", "failed"}}, float64(summary.Failed))
	// 'missed' is its own status label, not folded into 'failed': a scheduled
	// run that never executed is a distinct signal from one that ran and failed.
	writeSample(w, "runwisp_runs_total", []labelPair{{"status", "missed"}}, float64(summary.Missed))

	if summary.LastFailure != nil {
		writeHelpType(w, "runwisp_runs_last_failure_timestamp_seconds", "gauge", "Unix timestamp of the most recent failed run.")
		writeSample(w, "runwisp_runs_last_failure_timestamp_seconds", nil, float64(summary.LastFailure.Unix()))
	}

	writeHelpType(w, "runwisp_task_active_runs", "gauge", "Currently active runs per task.")
	for _, task := range tasks {
		count := srv.taskManager.GetActiveRunCount(task.Name)
		writeSample(w, "runwisp_task_active_runs", taskLabels(task), float64(count))
	}

	// Only tasks with a measured running shell run appear: an absent series
	// means "not running", not "using nothing".
	usage := srv.runService.usage()
	writeHelpType(w, "runwisp_task_cpu_percent", "gauge", "Live CPU use of a task's running processes, in percent of one core.")
	for _, task := range tasks {
		if u, ok := usage[task.Name]; ok {
			writeSample(w, "runwisp_task_cpu_percent", taskLabels(task), u.CPUPercent)
		}
	}
	writeHelpType(w, "runwisp_task_memory_bytes", "gauge", "Live resident memory of a task's running processes, in bytes.")
	for _, task := range tasks {
		if u, ok := usage[task.Name]; ok {
			writeSample(w, "runwisp_task_memory_bytes", taskLabels(task), float64(u.MemoryBytes))
		}
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

// taskLabels identifies a task series; kind defaults to "task" like the TOML.
func taskLabels(task model.Task) []labelPair {
	kind := string(task.Kind)
	if kind == "" {
		kind = "task"
	}
	return []labelPair{{"task", task.Name}, {"kind", kind}}
}

func writeHelpType(w io.Writer, name, metricType, help string) {
	fmt.Fprintf(w, "# HELP %s %s\n", name, helpEscaper.Replace(help))
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
		sb.WriteString(labelValueEscaper.Replace(l.value))
		sb.WriteByte('"')
	}
	sb.WriteByte('}')
	fmt.Fprintf(w, "%s %s\n", sb.String(), formatFloat(value))
}

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// labelValueEscaper escapes the three characters OpenMetrics requires for
// label values: backslash, double quote, and newline.
var labelValueEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)

// helpEscaper escapes backslash and newline in HELP text (double quotes are
// allowed unescaped in HELP per the OpenMetrics spec).
var helpEscaper = strings.NewReplacer(`\`, `\\`, "\n", `\n`)
