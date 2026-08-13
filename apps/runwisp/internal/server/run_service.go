// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runtime"
	"github.com/runwisp/runwisp/internal/storage"
)

var (
	ErrTaskNotFound          = errors.New("task not found")
	ErrAPIDisabled           = errors.New("API triggering disabled for this task")
	ErrRunNotFound           = errors.New("run not found")
	ErrNotRunning            = errors.New("run is not currently running")
	ErrServiceNotRunnable    = errors.New("services cannot be triggered; use the restart endpoint")
	ErrNotAService           = errors.New("task is not a service")
	ErrCannotDeleteActiveRun = errors.New("cannot delete a run that is still pending or running; stop it first")
	ErrInvalidSelector       = errors.New("invalid run selector")
	ErrInvalidParams         = errors.New("invalid parameters")
)

func wrapSelectorErr(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrInvalidSelector, err.Error())
}

type runService struct {
	db          storage.RunRepository
	taskManager runtime.TaskRunner
	tasks       *runtime.TaskRegistry
	scheduler   runtime.NextRunGetter
	logDir      string
	eventBus    *events.Bus
}

func newRunService(db storage.RunRepository, jm runtime.TaskRunner, tasks *runtime.TaskRegistry, sched runtime.NextRunGetter, logDir string, bus *events.Bus) *runService {
	return &runService{db: db, taskManager: jm, tasks: tasks, scheduler: sched, logDir: logDir, eventBus: bus}
}

func (s *runService) ListTasks() []model.TaskResponse {
	tasks := make([]model.TaskResponse, 0, s.tasks.Len())
	s.tasks.Range(func(_ string, task *model.Task) bool {
		tr := model.TaskResponse{Task: *task}
		if task.Cron != "" && s.scheduler != nil {
			tr.NextRunAt = s.scheduler.GetNextRun(task.Name)
		}
		tasks = append(tasks, tr)
		return true
	})
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].Name < tasks[j].Name })
	return tasks
}

// mapNotFound translates storage.ErrNotFound to ErrRunNotFound.
func mapNotFound(err error) error {
	if errors.Is(err, storage.ErrNotFound) {
		return ErrRunNotFound
	}
	return err
}

func (s *runService) ListRuns(ctx context.Context, taskName string, p PaginationParams) (*RunsResponseBody, error) {
	// A path task name (single-task view) overrides any task_name query param.
	filter := p.Filter
	if taskName != "" {
		filter.TaskName = taskName
	}
	runs, err := s.db.QueryRuns(ctx, storage.RunQuery{
		Filter:        filter,
		Limit:         p.Limit,
		Offset:        p.Offset,
		SortField:     p.SortField,
		SortDirection: p.SortDirection,
	})
	if err != nil {
		return nil, err
	}

	total, err := s.db.CountRunsFiltered(ctx, filter)
	if err != nil {
		return nil, err
	}

	return &RunsResponseBody{Items: runs, Total: total}, nil
}

func (s *runService) GetRun(ctx context.Context, runID string) (*model.Run, error) {
	run, err := s.db.GetRun(ctx, runID)
	if err != nil {
		return nil, mapNotFound(err)
	}
	return run, nil
}

func (s *runService) TriggerRun(ctx context.Context, taskName string, params map[string]*string) (*model.Run, error) {
	task, exists := s.tasks.Get(taskName)
	if !exists {
		return nil, ErrTaskNotFound
	}
	if task.Kind.IsService() {
		return nil, ErrServiceNotRunnable
	}
	if !task.APITrigger {
		return nil, ErrAPIDisabled
	}
	// Validate supplied values at the boundary so a bad value surfaces as a 400
	// (the manager re-resolves the same pure transform before persisting).
	if _, err := model.ResolveParamValues(task.Parameters, params); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidParams, err.Error())
	}
	return s.taskManager.TriggerRunWithOptions(taskName, runtime.TriggerRunOptions{
		TriggeredBy: model.TriggeredByAPI,
		Params:      params,
	})
}

// terminalWaitBackstop is how often TriggerRunAndWait re-reads storage while
// waiting. The in-memory event bus is best-effort (a slow consumer's event can
// be dropped) and persistence is async, so neither is authoritative alone —
// the poll is a cheap safety net that bounds worst-case latency if the live
// event is missed.
const terminalWaitBackstop = 2 * time.Second

// TriggerRunAndWait triggers a run and blocks until it reaches a terminal state
// or timeout elapses, returning the finished run (with exit_code / end_reason).
// On timeout it returns the run in its latest known — possibly still running —
// state; callers tell the two apart via the run's status. It exists so a remote
// caller can fire a task and read its result in a single request instead of
// trigger-then-poll.
func (s *runService) TriggerRunAndWait(ctx context.Context, taskName string, params map[string]*string, timeout time.Duration) (*model.Run, error) {
	// Subscribe before triggering: a fast task can finish and publish its
	// terminal event before TriggerRun even returns, so we must already be
	// listening. The handler forwards every terminal run; we filter by ID once
	// we know it.
	terminal := make(chan *model.Run, 64)
	forward := func(e events.Event) {
		if re, ok := e.Data.(events.RunEvent); ok && re.Run != nil {
			select {
			case terminal <- re.Run:
			default: // full — the backstop poll will catch our run
			}
		}
	}
	unsubDone := s.eventBus.Subscribe(events.EventRunCompleted, forward)
	defer unsubDone()
	unsubFailed := s.eventBus.Subscribe(events.EventRunFailed, forward)
	defer unsubFailed()

	run, err := s.TriggerRun(ctx, taskName, params)
	if err != nil {
		return nil, err
	}
	return s.awaitTerminal(ctx, run, terminal, timeout), nil
}

// awaitTerminal blocks until run reaches a terminal state, timeout elapses, or
// ctx is cancelled, returning the most authoritative run state it can find. It
// races three signals: the live terminal event (filtered to our run), a
// periodic storage re-read (backstop for a missed event), and the deadline.
func (s *runService) awaitTerminal(ctx context.Context, run *model.Run, terminal <-chan *model.Run, timeout time.Duration) *model.Run {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(terminalWaitBackstop)
	defer ticker.Stop()

	for {
		select {
		case r := <-terminal:
			if r.ID == run.ID {
				return r
			}
		case <-ticker.C:
			if fresh, err := s.db.GetRun(ctx, run.ID); err == nil && fresh.Status == model.PhaseEnded {
				return fresh
			}
		case <-waitCtx.Done():
			// Deadline reached or client disconnected. Hand back the latest
			// persisted state so the caller can see status != "ended".
			if fresh, err := s.db.GetRun(ctx, run.ID); err == nil {
				return fresh
			}
			return run
		}
	}
}

func (s *runService) RestartService(taskName string) error {
	task, exists := s.tasks.Get(taskName)
	if !exists {
		return ErrTaskNotFound
	}
	if !task.Kind.IsService() {
		return ErrNotAService
	}
	return s.taskManager.RestartServiceInstances(taskName)
}

func (s *runService) StopService(taskName string) error {
	task, exists := s.tasks.Get(taskName)
	if !exists {
		return ErrTaskNotFound
	}
	if !task.Kind.IsService() {
		return ErrNotAService
	}
	return s.taskManager.StopService(taskName)
}

func (s *runService) DeleteRun(ctx context.Context, runID string) error {
	run, err := s.db.GetRun(ctx, runID)
	if err != nil {
		return mapNotFound(err)
	}
	if run.Status == model.PhaseRunning || run.Status == model.PhasePending {
		return ErrCannotDeleteActiveRun
	}
	// Single-row delete is just a one-ID soft-delete — no duplicated logic.
	_, err = s.bulkSoftDelete(ctx, model.RunSelector{IDs: []string{runID}})
	return err
}

// bulkSoftDelete applies a selector against the soft-delete storage method
// and publishes run.deleted for every affected row.
func (s *runService) bulkSoftDelete(ctx context.Context, sel model.RunSelector) (int, error) {
	if err := sel.Validate(); err != nil {
		return 0, wrapSelectorErr(err)
	}
	refs, err := s.db.SoftDeleteRuns(ctx, sel, time.Now())
	if err != nil {
		return 0, err
	}
	for _, ref := range refs {
		s.publishDeleted(ref)
	}
	return len(refs), nil
}

// bulkRestore reverses a soft-delete and re-emits run.updated for each
// restored run so connected UIs can splice the rows back in.
func (s *runService) bulkRestore(ctx context.Context, sel model.RunSelector) (int, error) {
	if err := sel.Validate(); err != nil {
		return 0, wrapSelectorErr(err)
	}
	runs, err := s.db.RestoreRuns(ctx, sel)
	if err != nil {
		return 0, err
	}
	for i := range runs {
		s.publishRunUpdated(&runs[i])
	}
	return len(runs), nil
}

// bulkCancel resolves the selector to currently-running runs and terminates
// each one through the task manager. Returns the number of cancel signals
// sent (best-effort — a run may exit between resolve and signal).
func (s *runService) bulkCancel(ctx context.Context, sel model.RunSelector) (int, error) {
	if err := sel.Validate(); err != nil {
		return 0, wrapSelectorErr(err)
	}
	refs, err := s.db.ResolveSelectorIDs(ctx, sel, string(model.PhaseRunning))
	if err != nil {
		return 0, err
	}
	signalled := 0
	for _, ref := range refs {
		if err := s.taskManager.TerminateRun(ref.ID); err == nil {
			signalled++
		}
	}
	return signalled, nil
}

// bulkRerun resolves the selector to a unique set of task names and triggers
// one new run per task. Returns the (task_name, new_run_id) pairs so the UI
// can build an "undo rerun" selector.
func (s *runService) bulkRerun(ctx context.Context, sel model.RunSelector) ([]TriggeredRunRef, error) {
	if err := sel.Validate(); err != nil {
		return nil, wrapSelectorErr(err)
	}
	refs, err := s.db.ResolveSelectorIDs(ctx, sel, "")
	if err != nil {
		return nil, err
	}
	// Dedupe to one rerun per task, remembering a representative run so we can
	// carry its resolved params forward (mirroring retry/restart). Without this
	// a parameterised task can't be rerun at all — a required param has no
	// default to fall back on — and even when it could, replaying the operator's
	// original inputs is what "rerun" means.
	repByTask := map[string]string{}
	taskNames := make([]string, 0, len(refs))
	for _, ref := range refs {
		if _, ok := repByTask[ref.TaskName]; ok {
			continue
		}
		repByTask[ref.TaskName] = ref.ID
		taskNames = append(taskNames, ref.TaskName)
	}
	sort.Strings(taskNames)
	out := make([]TriggeredRunRef, 0, len(taskNames))
	for _, name := range taskNames {
		task, ok := s.tasks.Get(name)
		if !ok || task.Kind.IsService() || !task.APITrigger {
			continue
		}
		var params map[string]*string
		if prev, err := s.db.GetRun(ctx, repByTask[name]); err == nil && prev != nil {
			params = model.SuppliedFromResolved(task.Parameters, prev.Params)
		}
		run, err := s.taskManager.TriggerRunWithOptions(name, runtime.TriggerRunOptions{
			TriggeredBy: model.TriggeredByAPI,
			Params:      params,
		})
		if err != nil || run == nil {
			continue
		}
		out = append(out, TriggeredRunRef{TaskName: name, RunID: run.ID})
	}
	return out, nil
}

func (s *runService) publishDeleted(ref storage.RunRef) {
	if s.eventBus == nil {
		return
	}
	s.eventBus.Publish(events.EventRunDeleted, events.RunDeletedEvent{
		RunID:    ref.ID,
		TaskName: ref.TaskName,
	})
}

func (s *runService) publishRunUpdated(run *model.Run) {
	if s.eventBus == nil {
		return
	}
	s.eventBus.Publish(events.EventRunUpdated, events.RunEvent{Run: run.Copy()})
}

func (s *runService) StopRun(ctx context.Context, runID string) error {
	run, err := s.db.GetRun(ctx, runID)
	if err != nil {
		return mapNotFound(err)
	}
	if run.Status != model.PhaseRunning {
		return ErrNotRunning
	}
	return s.taskManager.TerminateRun(runID)
}
