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

func (mc *MetricsCollector) collect() {
	s := model.MetricsSample{Timestamp: time.Now().Unix()}
	if runtime.GOOS == "linux" {
		populateLinuxSample(&s)
	} else {
		populateFallbackSample(&s)
	}
	s.CPUUsage = float64(int(s.CPUUsage*10)) / 10
	s.MemUsage = float64(int(s.MemUsage*10)) / 10

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

func populateLinuxSample(s *model.MetricsSample) {
	memTotal, memAvailable := getMemInfo()
	if memTotal > 0 {
		s.MemTotal = memTotal
		s.MemUsed = memTotal - memAvailable
		s.MemUsage = float64(s.MemUsed) / float64(s.MemTotal) * 100
	}

	load1 := getLoadAvg()
	usage := (load1 / float64(runtime.NumCPU())) * 100
	if usage > 100 {
		usage = 100
	}
	s.CPUUsage = usage
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
