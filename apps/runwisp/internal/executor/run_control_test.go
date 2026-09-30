// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"context"
	"errors"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/model"
)

// scriptedBackend stands in for the shell backend: a task named "svc" runs
// until its ctx ends, a probe prints output and exits with exitCode (or hangs
// until its ctx ends when hang is set, or fails to start when startErr is set).
type scriptedBackend struct {
	output   string
	exitCode int
	hang     bool
	startErr error
}

func (b *scriptedBackend) Available(context.Context) bool { return true }

func (b *scriptedBackend) Start(ctx context.Context, task *model.Task, _ *model.Run, _ model.ExecutionDef) (*Process, error) {
	if task.Name == "svc" {
		return &Process{
			Stdout: io.NopCloser(strings.NewReader("")),
			Wait: func() (int, error) {
				<-ctx.Done()
				return -1, ctx.Err()
			},
		}, nil
	}
	if b.startErr != nil {
		return nil, b.startErr
	}
	return &Process{
		Stdout: io.NopCloser(strings.NewReader(b.output)),
		Wait: func() (int, error) {
			if b.hang {
				<-ctx.Done()
				return -1, ctx.Err()
			}
			return b.exitCode, nil
		},
	}, nil
}

func newScriptedExecutor(t *testing.T, backend *scriptedBackend) (*RoutingExecutor, *events.Bus) {
	t.Helper()
	eb := events.NewEventBus()
	r, ok := New(Options{LogDir: t.TempDir(), EventBus: eb, HasLocalTasks: true}).(*RoutingExecutor)
	require.True(t, ok)
	r.backends["shell"] = backend
	return r, eb
}

func newRun() *model.Run {
	return &model.Run{ID: ulid.Make().String(), Status: model.PhaseRunning}
}

func TestRunWatcher_ProbeAnnotateKill(t *testing.T) {
	r, eb := newScriptedExecutor(t, &scriptedBackend{output: "token=hunter2 down\n", exitCode: 3})
	getLogPath := captureLogPath(eb)
	var (
		mu    sync.Mutex
		lines []string
	)
	eb.Subscribe(events.EventLogLine, func(e events.Event) {
		if l, ok := e.Data.(events.LogLineEvent); ok && l.Stream == logutil.StreamSystem {
			mu.Lock()
			lines = append(lines, l.Text)
			mu.Unlock()
		}
	})

	var probed ProbeResult
	r.SetRunWatcher(func(ctx context.Context, _ *model.Task, _ *model.Run, ctl RunControl) {
		probed = ctl.Probe(ctx, &model.Task{Name: "svc.health_check", Run: "check", Secrets: map[string]string{"T": "hunter2"}})
		ctl.Annotate("probe said hunter2")
		ctl.Kill(model.ReasonUnhealthy)
		ctl.Kill(model.ReasonLogOverflow)
	})

	task := &model.Task{Name: "svc", Run: "serve", Secrets: map[string]string{"T": "hunter2"}}
	res := r.Execute(context.Background(), task, newRun())

	assert.Equal(t, model.ReasonUnhealthy, res.EndReason(), "the first kill reason wins")
	assert.Equal(t, 3, probed.ExitCode)
	assert.NotContains(t, probed.Output, "hunter2", "probe output is redacted")
	assert.Contains(t, probed.Output, "down")

	log, err := os.ReadFile(getLogPath())
	require.NoError(t, err)
	assert.Contains(t, string(log), "probe said")
	assert.NotContains(t, string(log), "hunter2", "annotations are redacted")
	assert.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, l := range lines {
			if strings.Contains(l, "probe said") {
				return true
			}
		}
		return false
	}, time.Second, 5*time.Millisecond, "annotations reach the live stream")
}

// The watcher is cancelled when the run ends and joined before the log closes,
// so whatever it reports on the way out still lands in the run's log.
func TestRunWatcher_JoinedBeforeLogCloses(t *testing.T) {
	r, eb := newScriptedExecutor(t, &scriptedBackend{})
	getLogPath := captureLogPath(eb)
	r.SetRunWatcher(func(ctx context.Context, _ *model.Task, _ *model.Run, ctl RunControl) {
		<-ctx.Done()
		time.Sleep(20 * time.Millisecond)
		ctl.Annotate("watcher done")
		ctl.Kill(model.ReasonUnhealthy)
	})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	res := r.Execute(ctx, &model.Task{Name: "svc", Run: "serve"}, newRun())

	assert.Equal(t, model.ReasonStopped, res.EndReason(), "a kill after the run ended can't relabel it")
	log, err := os.ReadFile(getLogPath())
	require.NoError(t, err)
	assert.Contains(t, string(log), "watcher done")
}

func TestProbe_OutputTailIsCapped(t *testing.T) {
	out := strings.Repeat("x", 2*probeOutputTail) + "the end"
	r, _ := newScriptedExecutor(t, &scriptedBackend{output: out})

	res := r.probe(context.Background(), &model.Task{Name: "p", Run: "check"}, newRun())

	assert.Len(t, res.Output, probeOutputTail)
	assert.True(t, strings.HasSuffix(res.Output, "the end"))
	assert.Equal(t, model.ReasonSuccess, res.EndReason())
}

func TestProbe_OutputPatternFailsExitZero(t *testing.T) {
	r, _ := newScriptedExecutor(t, &scriptedBackend{output: "ok\nDB error: hunter2 rejected\n"})
	probe := &model.Task{
		Name:     "p",
		Run:      "check",
		Secrets:  map[string]string{"PW": "hunter2"},
		Failures: model.FailureMatcher{OutputPatterns: []string{"hunter2", "error: " + regexp.QuoteMeta(redactMask)}},
	}

	res := r.probe(context.Background(), probe, newRun())

	assert.Equal(t, model.ReasonFailed, res.EndReason())
	assert.Equal(t, "error: "+regexp.QuoteMeta(redactMask), res.MatchedPattern, "patterns see redacted output, like a run's log")
	assert.True(t, probe.IsFailureReason(res.EndReason(), res.ExitCode, res.OutputMatched))
}

func TestProbe_OutputPatternNoMatchSucceeds(t *testing.T) {
	r, _ := newScriptedExecutor(t, &scriptedBackend{output: "all good\n"})
	probe := &model.Task{Name: "p", Run: "check", Failures: model.FailureMatcher{OutputPatterns: []string{"error"}}}

	res := r.probe(context.Background(), probe, newRun())

	assert.Equal(t, model.ReasonSuccess, res.EndReason())
	assert.Empty(t, res.MatchedPattern)
}

func TestProbe_Timeout(t *testing.T) {
	r, _ := newScriptedExecutor(t, &scriptedBackend{hang: true})
	timeout := 10 * time.Millisecond

	res := r.probe(context.Background(), &model.Task{Name: "p", Run: "check", Timeout: &timeout}, newRun())

	assert.Equal(t, model.ReasonTimeout, res.EndReason())
}

func TestProbe_StartError(t *testing.T) {
	r, _ := newScriptedExecutor(t, &scriptedBackend{startErr: errors.New("no shell")})

	res := r.probe(context.Background(), &model.Task{Name: "p", Run: "check"}, newRun())

	require.Error(t, res.Error)
	assert.Contains(t, res.Error.Error(), "no shell")
	assert.Equal(t, model.ReasonFailed, res.EndReason())
}

func TestTailBuffer_DropsSplitRune(t *testing.T) {
	tb := &tailBuffer{max: 4}
	_, _ = tb.Write([]byte("aé€")) // a(1) é(2) €(3): the cut lands inside é
	assert.Equal(t, "€", tb.String())
}
