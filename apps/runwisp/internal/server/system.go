// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"bufio"
	"context"
	"fmt"
	"maps"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/version"
)

const AppName = "runwisp"

type statsProvider struct {
	daemonInfo *model.DaemonInfo
	startTime  time.Time
}

func newStatsProvider(daemonInfo *model.DaemonInfo, startTime time.Time) *statsProvider {
	return &statsProvider{
		daemonInfo: daemonInfo,
		startTime:  startTime,
	}
}

func (p *statsProvider) GetSystemStats() model.SystemStats {
	stats := model.SystemStats{
		Version:  version.Version,
		Name:     AppName,
		CPUCores: runtime.NumCPU(),
		OS:       runtime.GOOS,
		Arch:     runtime.GOARCH,
	}

	if host, err := os.Hostname(); err == nil {
		stats.Host = host
	} else {
		stats.Host = "unknown"
	}

	if wd, err := os.Getwd(); err == nil {
		stats.WorkDir = wd
	}

	stats.Uptime = formatUptime(time.Since(p.startTime))

	if runtime.GOOS == "linux" {
		populateLinuxStats(&stats)
	} else {
		populateFallbackStats(&stats)
	}

	stats.CPUUsage = float64(int(stats.CPUUsage*10)) / 10
	stats.MemUsage = float64(int(stats.MemUsage*10)) / 10

	return stats
}

func (p *statsProvider) GetDaemonInfo() *model.DaemonInfo {
	if p.daemonInfo == nil {
		return &model.DaemonInfo{}
	}
	return p.daemonInfo
}

func (srv *Server) humaGetInfo(ctx context.Context, input *struct{}) (*DaemonInfoOutput, error) {
	info := *srv.stats.GetDaemonInfo()
	// Staleness is probed per request — the browser can't read the daemon's
	// disk, and a cached answer would defeat the point of the indicator.
	if srv.configStale != nil {
		info.ConfigStale = srv.configStale()
	}
	// Same reasoning as staleness: a reload can add or clear a warning, and the
	// DaemonInfo the provider holds was built at boot.
	if srv.configWarnings != nil {
		info.ConfigWarnings = srv.configWarnings()
	}
	// And the task list itself, for the same reason once more. A reload adds,
	// removes and changes tasks, and the cron hold releases itself the moment a
	// system cron daemon goes away — so the boot-time list would keep reporting a
	// job as held by cron long after RunWisp took it over, on the two surfaces that
	// read it (`runwisp status` and the TUI header). Cheap: a registry snapshot and
	// a field copy per task.
	if tasks := srv.currentTaskBriefs(); tasks != nil {
		info.Tasks = tasks
	}
	return &DaemonInfoOutput{Body: info}, nil
}

// currentTaskBriefs rebuilds /api/info's task list from the live registry, in the
// same name order the boot path used. nil when there is no registry to read, which
// leaves the boot-time list in place.
func (srv *Server) currentTaskBriefs() []model.TaskBrief {
	if srv.tasks == nil {
		return nil
	}
	snapshot := srv.tasks.Snapshot()
	briefs := make([]model.TaskBrief, 0, len(snapshot))
	for _, name := range slices.Sorted(maps.Keys(snapshot)) {
		briefs = append(briefs, model.NewTaskBrief(snapshot[name]))
	}
	return briefs
}

func (srv *Server) humaGetSystemStats(ctx context.Context, input *struct{}) (*SystemStatsOutput, error) {
	stats := srv.stats.GetSystemStats()
	return &SystemStatsOutput{Body: stats}, nil
}

// currentConfigStale probes on-disk staleness, treating a nil hook (modes that
// can't reload) as never-stale.
func (srv *Server) currentConfigStale() bool {
	if srv.configStale == nil {
		return false
	}
	return srv.configStale()
}

// broadcastSample fans a freshly collected metrics sample out over the event
// bus as a system event, and — only when staleness has flipped since the last
// tick — a config.stale event. It runs on the metrics collector goroutine, so
// it owns configStaleLast exclusively (no lock needed). This is the single
// server-side replacement for every dashboard polling /api/system + /api/info.
func (srv *Server) broadcastSample(sample model.MetricsSample) {
	if srv.eventBus == nil {
		return
	}
	srv.eventBus.Publish(events.EventSystemSample, events.SystemSampleEvent{
		Sample: sample,
		Uptime: formatUptime(time.Since(srv.stats.startTime)),
	})

	stale := srv.currentConfigStale()
	if stale != srv.configStaleLast {
		srv.configStaleLast = stale
		srv.eventBus.Publish(events.EventConfigStale, events.ConfigStaleEvent{Stale: stale})
	}
}

// humaReload reconciles the live task set against runwisp.toml. A nil reload
// hook means the daemon was started in a mode that can't reload (cloud mode has
// no local scheduler); a rejected reload (bad config or a restart-only change)
// surfaces as a 400 so the operator sees exactly why nothing was applied.
func (srv *Server) humaReload(ctx context.Context, input *struct{}) (*ReloadOutput, error) {
	if srv.reload == nil {
		return nil, huma.Error400BadRequest("reload is not available in this mode")
	}
	result, err := srv.reload()
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	return &ReloadOutput{Body: result}, nil
}

// SystemStats returns a live host snapshot, identical to what the local
// /api/system endpoint serves. Exposed so the cloud client can piggyback the
// snapshot on its heartbeat without standing up the HTTP surface separately.
func (srv *Server) SystemStats() model.SystemStats {
	return srv.stats.GetSystemStats()
}

func (srv *Server) humaGetMetricsHistory(ctx context.Context, input *struct{}) (*MetricsHistoryOutput, error) {
	return &MetricsHistoryOutput{Body: srv.metrics.History()}, nil
}

func getMemInfo() (uint64, uint64) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	var total, available uint64
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		val, _ := strconv.ParseUint(parts[1], 10, 64)
		if strings.HasPrefix(parts[0], "MemTotal") {
			total = val * 1024 // kB to B
		} else if strings.HasPrefix(parts[0], "MemAvailable") {
			available = val * 1024
		}
	}
	return total, available
}

func getLoadAvg() float64 {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(data))
	if len(fields) > 0 {
		val, _ := strconv.ParseFloat(fields[0], 64)
		return val
	}
	return 0
}

func formatUptime(duration time.Duration) string {
	days := int(duration.Hours()) / 24
	hours := int(duration.Hours()) % 24
	minutes := int(duration.Minutes()) % 60

	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}

func populateLinuxStats(stats *model.SystemStats) {
	var s model.MetricsSample
	populateLinuxSample(&s)
	stats.MemTotal = s.MemTotal
	stats.MemUsed = s.MemUsed
	stats.MemUsage = s.MemUsage
	stats.CPUUsage = s.CPUUsage
}

func populateFallbackStats(stats *model.SystemStats) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	populateFallbackStatsFromMemStats(stats, &m)
}

// populateFallbackStatsFromMemStats is the deterministic core of
// populateFallbackStats; see populateFallbackSampleFromMemStats.
func populateFallbackStatsFromMemStats(stats *model.SystemStats, m *runtime.MemStats) {
	var s model.MetricsSample
	populateFallbackSampleFromMemStats(&s, m)
	stats.MemTotal = s.MemTotal
	stats.MemUsed = s.MemUsed
	stats.MemUsage = s.MemUsage
	stats.CPUUsage = s.CPUUsage
}
