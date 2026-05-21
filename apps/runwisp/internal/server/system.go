// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

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
	return &DaemonInfoOutput{Body: *srv.stats.GetDaemonInfo()}, nil
}

func (srv *Server) humaGetSystemStats(ctx context.Context, input *struct{}) (*SystemStatsOutput, error) {
	stats := srv.stats.GetSystemStats()
	return &SystemStatsOutput{Body: stats}, nil
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
