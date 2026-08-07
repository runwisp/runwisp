// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"runtime"
	"sync"
	"time"

	"github.com/runwisp/runwisp/internal/model"
)

// MetricsCollector periodically samples system resource usage into a
// fixed-capacity ring buffer. All methods are safe for concurrent use.
type MetricsCollector struct {
	mu      sync.RWMutex
	samples []model.MetricsSample
	maxSize int
	stop    chan struct{}

	// onSample, when set, is invoked with each freshly collected sample after it
	// lands in the ring. It runs on the collector goroutine, so it must not
	// block; the server uses it to fan the sample out over the event bus. Set
	// before Start (no synchronization with the collector goroutine otherwise).
	onSample func(model.MetricsSample)
}

// NewMetricsCollector creates a collector that retains up to maxSize samples.
func NewMetricsCollector(maxSize int) *MetricsCollector {
	return &MetricsCollector{
		samples: make([]model.MetricsSample, 0, maxSize),
		maxSize: maxSize,
		stop:    make(chan struct{}),
	}
}

// Start begins periodic collection at the given interval.
// It records an initial sample immediately.
func (mc *MetricsCollector) Start(interval time.Duration) {
	mc.collect()
	go mc.loop(interval)
}

// Stop terminates the collection goroutine.
func (mc *MetricsCollector) Stop() {
	close(mc.stop)
}

// History returns a copy of all collected samples (oldest first).
func (mc *MetricsCollector) History() []model.MetricsSample {
	mc.mu.RLock()
	defer mc.mu.RUnlock()
	out := make([]model.MetricsSample, len(mc.samples))
	copy(out, mc.samples)
	return out
}

func (mc *MetricsCollector) loop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			mc.collect()
		case <-mc.stop:
			return
		}
	}
}

// round1 truncates x to one decimal place, the resolution both the metrics
// ring and /api/system report CPU/memory percentages at.
func round1(x float64) float64 {
	return float64(int(x*10)) / 10
}

func (mc *MetricsCollector) collect() {
	s := model.MetricsSample{Timestamp: time.Now().Unix()}
	populatePlatformSample(&s)
	s.CPUUsage = round1(s.CPUUsage)
	s.MemUsage = round1(s.MemUsage)

	mc.mu.Lock()
	mc.samples = append(mc.samples, s)
	if len(mc.samples) > mc.maxSize {
		mc.samples = mc.samples[len(mc.samples)-mc.maxSize:]
	}
	mc.mu.Unlock()

	// Fan out after releasing the lock so a slow subscriber never stalls
	// collection or History readers.
	if mc.onSample != nil {
		mc.onSample(s)
	}
}

// populatePlatformStats fills a one-shot SystemStats snapshot from the same
// per-OS collector the ring buffer uses, so /api/system and the metrics history
// never disagree. Platform files supply populatePlatformSample.
func populatePlatformStats(stats *model.SystemStats) {
	var s model.MetricsSample
	populatePlatformSample(&s)
	stats.MemTotal = s.MemTotal
	stats.MemUsed = s.MemUsed
	stats.MemUsage = s.MemUsage
	stats.CPUUsage = s.CPUUsage
}

func populateFallbackSample(s *model.MetricsSample) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	populateFallbackSampleFromMemStats(s, &m)
}

// populateFallbackSampleFromMemStats is the deterministic core of
// populateFallbackSample, split out so tests can pass synthetic MemStats
// instead of racing against the live allocator.
func populateFallbackSampleFromMemStats(s *model.MetricsSample, m *runtime.MemStats) {
	s.MemTotal = m.Sys
	s.MemUsed = m.Alloc
}
