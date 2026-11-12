// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package station

import (
	"context"

	"github.com/runwisp/runwisp/apps/runwisp/internal/events"
	"github.com/runwisp/runwisp/apps/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/apps/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/storage"
)

const (
	// serviceLogRuns is how many of an instance's newest runs a tail reaches
	// back into.
	serviceLogRuns = 5
	// serviceLogScanPage and serviceLogScanMax bound the search for those
	// runs among the service's runs of every instance.
	serviceLogScanPage = 100
	serviceLogScanMax  = 1000
)

// streamServiceLogs serves service:logs: one service instance's output across
// its runs, each run streamed like log:subscribe with its run ID on every
// frame. A tail reaches back into earlier runs when the newest is shorter;
// follow continues into each new run until log:unsubscribe.
func (s *session) streamServiceLogs(ctx context.Context, id string, req protocol.ServiceLogs) (any, error) {
	name, err := s.serviceName(req.TaskID)
	if err != nil {
		return nil, err
	}
	ctx, done := s.track(ctx, id)
	defer done()

	// Subscribe before the query: a run that starts in between is in both,
	// and streamed once.
	var started <-chan *model.Run
	if req.Follow {
		var stop func()
		started, stop = followInstance(s.client.deps.EventBus, name, req.Instance)
		defer stop()
	}
	runs, err := s.instanceRuns(ctx, name, req.Instance)
	if err != nil {
		return nil, &StationError{Code: protocol.ErrorCodeInternal, Message: "failed to look up the service's runs", Retryable: true, Err: err}
	}

	// queried holds the runs streamed below; a start event for one of them
	// raced the query and is skipped.
	queried := make(map[string]bool, len(runs))
	res := protocol.LogSubscribeResult{}
	oldest, from := s.tailStart(runs, req.FromLine)
	for i := oldest; i >= 0 && ctx.Err() == nil; i-- {
		queried[runs[i].ID] = true
		res = s.streamRun(ctx, id, &runs[i], from, 0, req.Follow && i == 0, runs[i].ID)
		from = 0
	}
	// A followed run that ended short of eof means the session is gone.
	if !req.Follow || ctx.Err() != nil || (len(runs) > 0 && !res.Eof) {
		return res, nil
	}
	return s.streamNewRuns(ctx, id, started, queried, res), nil
}

// serviceName resolves service:logs' taskId to a registered service.
func (s *session) serviceName(taskID string) (string, error) {
	name, _, err := s.client.handler.resolveServiceTarget(taskID, "")
	if err != nil {
		return "", err
	}
	task, ok := s.client.deps.TaskManager.GetTask(name)
	switch {
	case name == "" || !ok:
		return "", newError(protocol.ErrorCodeNotFound, "service %q not found", taskID)
	case !task.Kind.IsService():
		return "", newError(protocol.ErrorCodeConflict, "task %q is not a service", name)
	}
	return name, nil
}

// followInstance subscribes to the starts of one service instance's runs.
func followInstance(bus EventSubscriber, task string, instance int) (<-chan *model.Run, func()) {
	started := make(chan *model.Run, 16)
	stop := bus.Subscribe(events.EventRunStarted, func(e events.Event) {
		if re, ok := e.Data.(events.RunEvent); ok && re.Run != nil && re.Run.TaskName == task && re.Run.InstanceIndex == instance {
			pushEvictingOldest(started, re.Run)
		}
	})
	return started, stop
}

// streamNewRuns streams each run that starts, skipping those already
// streamed, until the subscription is cancelled or a stream ends short.
func (s *session) streamNewRuns(ctx context.Context, id string, started <-chan *model.Run, skip map[string]bool, res protocol.LogSubscribeResult) protocol.LogSubscribeResult {
	for {
		select {
		case <-ctx.Done():
			return res
		case run := <-started:
			if skip[run.ID] || ctx.Err() != nil {
				continue // already streamed, or cancelled: ctx.Done wins next
			}
			if res = s.streamRun(ctx, id, run, 0, 0, true, run.ID); !res.Eof {
				return res
			}
		}
	}
}

// tailStart picks where to start among runs (newest first): the oldest run to
// stream (-1 for none) and the line to start it at; every newer run streams
// from 0. fromLine >= 0 starts the newest run there. A tail takes what it can
// from the newest run and reaches into older ones for the rest.
func (s *session) tailStart(runs []model.Run, fromLine int64) (oldest int, from int64) {
	switch {
	case len(runs) == 0:
		return -1, 0
	case fromLine >= 0:
		return 0, fromLine
	}
	want := -fromLine
	for ; oldest < len(runs)-1; oldest++ {
		run := runs[oldest]
		have := availableLines(logutil.ResolveRunLogPath(s.client.deps.LogDir, run.TaskName, run.ID, run.CreatedAt))
		if have >= want {
			break
		}
		want -= have
	}
	return oldest, -want
}

// availableLines counts the lines of a run's log still on disk.
func availableLines(path string) int64 {
	_, first, total, err := logutil.ReadLineRange(path, 0, 1)
	if err != nil {
		return 0
	}
	return total - first
}

// instanceRuns returns up to serviceLogRuns of the instance's newest runs,
// newest first.
// ponytail: runs have no instance filter, so this pages through the service's
// newest serviceLogScanMax runs; add an instance filter to RunFilter if a
// service with many instances outgrows that.
func (s *session) instanceRuns(ctx context.Context, task string, instance int) ([]model.Run, error) {
	var out []model.Run
	for offset := 0; offset < serviceLogScanMax; offset += serviceLogScanPage {
		page, err := s.client.deps.RunRepo.QueryRuns(ctx, storage.RunQuery{Filter: model.RunFilter{TaskName: task}, Limit: serviceLogScanPage, Offset: offset})
		if err != nil {
			return nil, err
		}
		for _, run := range page {
			if run.InstanceIndex == instance {
				out = append(out, run)
				if len(out) == serviceLogRuns {
					return out, nil
				}
			}
		}
		if len(page) < serviceLogScanPage {
			break
		}
	}
	return out, nil
}
