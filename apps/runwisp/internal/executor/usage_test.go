// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"context"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/runwisp/runwisp/apps/runwisp/internal/events"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/procstat"
	"github.com/stretchr/testify/assert"
)

func TestPickPeak(t *testing.T) {
	ptr := func(n int64) *int64 { return &n }
	cases := []struct {
		name          string
		sampled       *int64
		child, daemon int64
		want          *int64
	}{
		{"child at the daemon's floor is ignored", ptr(5), 100, 100, ptr(5)},
		{"child below the floor is ignored", ptr(5), 80, 100, ptr(5)},
		{"floored and never sampled is unknown", nil, 100, 100, nil},
		{"child above the floor is its own peak", ptr(5), 300, 100, ptr(300)},
		{"child above the floor, unsampled", nil, 300, 100, ptr(300)},
		{"sampled peak never loses to a smaller child", ptr(400), 300, 100, ptr(400)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, pickPeak(tc.sampled, tc.child, tc.daemon))
		})
	}
}

// A backend with no process group (HTTP, container, compose) reports no usage.
func TestTrackWithoutPidHasNoUsage(t *testing.T) {
	p := &Process{}
	e := &RoutingExecutor{sampler: procstat.New()}
	peak, ok := e.track(&model.Task{Name: "t"}, &model.Run{ID: "r"}, p)()
	assert.False(t, ok)
	assert.Zero(t, peak)
	pm, cpu := runUsage(nil, peak, ok)
	assert.Nil(t, pm)
	assert.Nil(t, cpu)
}

// A running run's printed lines are readable while it runs, and it drops out
// of RunOutputLines once it has ended.
func TestRunOutputLinesWhileRunning(t *testing.T) {
	e := New(Options{LogDir: t.TempDir(), EventBus: events.NewEventBus(), HasLocalTasks: true})
	run := &model.Run{ID: ulid.Make().String(), Status: model.PhaseRunning}
	done := make(chan *ExecuteResult)
	go func() {
		done <- e.Execute(context.Background(), &model.Task{Name: "t", Run: "echo a; echo b >&2; sleep 1"}, run)
	}()

	assert.Eventually(t, func() bool { return e.RunOutputLines()[run.ID] == 2 }, 5*time.Second, 10*time.Millisecond)
	<-done
	assert.NotContains(t, e.RunOutputLines(), run.ID)
}
