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
	ErrManualTriggerDisabled = errors.New("manual_trigger is false: manual control is disabled for this task/service")
	ErrRunNotFound           = errors.New("run not found")
	ErrNotRunning            = errors.New("run is not currently running")
	ErrServiceNotRunnable    = errors.New("services cannot be triggered; use the restart endpoint")
	ErrCannotDeleteActiveRun = errors.New("cannot delete a run that is still pending or running; stop it first")
	ErrInvalidSelector       = errors.New("invalid run selector")
	ErrInvalidParams         = errors.New("invalid parameters")
	// ErrRestartDidNotDrain means a task's active run(s) didn't end within its
	// graceful-stop window (plus restartDrainGrace) after RestartTask's StopTask
	// call, so it gave up rather than trigger a fresh run alongside a
	// still-dying one.
	ErrRestartDidNotDrain = errors.New("previous run did not stop in time")
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
		tasks = append(tasks, s.toTaskResponse(task))
		return true
	})
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].Name < tasks[j].Name })
	return tasks
}

func (s *runService) GetTask(name string) (*model.TaskResponse, error) {
	task, ok := s.tasks.Get(name)
	if !ok {
		return nil, ErrTaskNotFound
	}
	tr := s.toTaskResponse(task)
	return &tr, nil
}

func (s *runService) toTaskResponse(task *model.Task) model.TaskResponse {
	tr := model.TaskResponse{Task: *task}
	if task.Cron != "" && s.scheduler != nil {
		tr.NextRunAt = s.scheduler.GetNextRun(task.Name)
	}
	return tr
}

// mapNotFound translates storage.ErrNotFound to ErrRunNotFound.
func mapNotFound(err error) error {
	if errors.Is(err, storage.ErrNotFound) {
		return ErrRunNotFound
	}
	return err
}

func (s *runService) ListRuns(ctx context.Context, p PaginationParams) (*RunsResponseBody, error) {
	filter := p.Filter
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

// viaToTriggeredBy maps the TriggerRunInput.Via query param to a TriggeredBy
// value. Empty (the plain-REST-caller default) is model.TriggeredByAPI; huma's
// enum tag on Via already rejects anything else at the HTTP boundary, so an
// unrecognized value here means a programming bug and falls back to API too.
func viaToTriggeredBy(via string) model.TriggeredBy {
	switch via {
	case "ui":
		return model.TriggeredByUI
	case "cli":
		return model.TriggeredByCLI
	default:
		return model.TriggeredByAPI
	}
}

func (s *runService) TriggerRun(ctx context.Context, taskName string, params map[string]*string, triggeredBy model.TriggeredBy) (*model.Run, error) {
	task, exists := s.tasks.Get(taskName)
	if !exists {
		return nil, ErrTaskNotFound
	}
	switch task.CheckTrigger() {
	case model.TriggerBlockedService:
		return nil, ErrServiceNotRunnable
	case model.TriggerBlockedManualDisabled:
		return nil, ErrManualTriggerDisabled
	}
	// Validate supplied values at the boundary so a bad value surfaces as a 400
	// (the manager re-resolves the same pure transform before persisting).
	if _, err := model.ResolveParamValues(task.Parameters, params); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidParams, err.Error())
	}
	return s.taskManager.TriggerRunWithOptions(taskName, runtime.TriggerRunOptions{
		TriggeredBy: triggeredBy,
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
func (s *runService) TriggerRunAndWait(ctx context.Context, taskName string, params map[string]*string, triggeredBy model.TriggeredBy, timeout time.Duration) (*model.Run, error) {
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

	run, err := s.TriggerRun(ctx, taskName, params, triggeredBy)
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

// resolveControllableTask looks up a task or service and checks the
// manual_trigger lock that StartTask, StopTask, and RestartTask all share
// regardless of kind, so the trio can't drift apart.
func (s *runService) resolveControllableTask(taskName string) (*model.Task, error) {
	task, exists := s.tasks.Get(taskName)
	if !exists {
		return nil, ErrTaskNotFound
	}
	if !task.ManuallyControllable() {
		return nil, ErrManualTriggerDisabled
	}
	return task, nil
}

// StartTask starts a service or triggers a task. A service un-parks (if
// operator-stopped) and fills empty instance slots; already-running instances
// are left alone. A task with a run already active or queued no-ops instead
// of piling up a second execution; otherwise it triggers exactly one fresh
// run, identical to TriggerRun.
func (s *runService) StartTask(ctx context.Context, taskName string, triggeredBy model.TriggeredBy) error {
	task, err := s.resolveControllableTask(taskName)
	if err != nil {
		return err
	}
	if task.Kind.IsService() {
		return s.taskManager.StartService(taskName)
	}
	// ponytail: GetActiveRunCount only counts ts.active, not a PolicyQueue
	// task's ts.queue, so a queued run mid-handoff to a freed slot can read as
	// 0 for an instant. Worst case here is a redundant queued run in that
	// narrow window — not worth a dedicated queued-count method for it.
	if s.taskManager.GetActiveRunCount(taskName) > 0 {
		return nil
	}
	_, err = s.TriggerRun(ctx, taskName, nil, triggeredBy)
	return err
}

// StopTask cancels a service's live instances or a task's active and queued
// runs. Neither touches the task's schedule — only in-flight executions are
// cut short.
func (s *runService) StopTask(taskName string) error {
	task, err := s.resolveControllableTask(taskName)
	if err != nil {
		return err
	}
	if task.Kind.IsService() {
		return s.taskManager.StopService(taskName)
	}
	return s.taskManager.StopTask(taskName)
}

// restartDrainGrace pads a task's configured graceful-stop window so
// RestartTask's wait outlives the executor's own kill-after-timeout path
// instead of racing it.
const restartDrainGrace = 5 * time.Second

// RestartTask restarts a service's instances or, for a task, stops its active
// runs, waits for them to actually end, then triggers exactly one fresh run —
// done server-side so a concurrency=skip policy can't swallow the new trigger
// racing the old run's teardown.
func (s *runService) RestartTask(ctx context.Context, taskName string, triggeredBy model.TriggeredBy) error {
	task, err := s.resolveControllableTask(taskName)
	if err != nil {
		return err
	}
	if task.Kind.IsService() {
		return s.taskManager.RestartServiceInstances(taskName)
	}
	if err := s.taskManager.StopTask(taskName); err != nil {
		return err
	}
	waitCtx, cancel := context.WithTimeout(ctx, task.GracefulStopValue()+restartDrainGrace)
	defer cancel()
	if runtime.WaitIdle(waitCtx, s.taskManager, taskName) != nil {
		return ErrRestartDidNotDrain
	}
	_, err = s.TriggerRun(ctx, taskName, nil, triggeredBy)
	return err
}

func (s *runService) DeleteRun(ctx context.Context, runID string) error {
	run, err := s.db.GetRun(ctx, runID)
	if err != nil {
		return mapNotFound(err)
	}
	if run.Status == model.PhaseRunning || run.Status == model.PhasePending {
		return ErrCannotDeleteActiveRun
	}
	// Single-row delete is just a one-ID soft-delete — no duplicated logic. The
	// run is already known terminal (guarded above), so skipped is always empty.
	_, _, err = s.bulkSoftDelete(ctx, model.RunSelector{IDs: []string{runID}})
	return err
}

// bulkSoftDelete applies a selector against the soft-delete storage method and
// publishes run.deleted for every affected row. SoftDeleteRuns only touches
// terminal rows, so any pending/running run in the selector is left in place;
// their IDs are returned as skipped so the caller can report the skip instead
// of silently dropping them.
func (s *runService) bulkSoftDelete(ctx context.Context, sel model.RunSelector) (int, []string, error) {
	if err := sel.Validate(); err != nil {
		return 0, nil, wrapSelectorErr(err)
	}
	skipped, err := s.resolveActiveIDs(ctx, sel)
	if err != nil {
		return 0, nil, err
	}
	refs, err := s.db.SoftDeleteRuns(ctx, sel, time.Now())
	if err != nil {
		return 0, nil, err
	}
	for _, ref := range refs {
		s.publishDeleted(ref)
	}
	return len(refs), skipped, nil
}

// resolveActiveIDs returns the IDs of selector-matched runs that are still
// pending or running — exactly the rows a soft-delete leaves untouched. The
// storage status filter matches one phase at a time, so the two active phases
// are resolved separately and unioned.
func (s *runService) resolveActiveIDs(ctx context.Context, sel model.RunSelector) ([]string, error) {
	var ids []string
	for _, phase := range []model.RunPhase{model.PhasePending, model.PhaseRunning} {
		refs, err := s.db.ResolveSelectorIDs(ctx, sel, string(phase))
		if err != nil {
			return nil, err
		}
		for _, ref := range refs {
			ids = append(ids, ref.ID)
		}
	}
	return ids, nil
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
		if !ok || !task.Triggerable() {
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
