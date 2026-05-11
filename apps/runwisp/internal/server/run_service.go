// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"errors"
	"sort"

	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runtime"
	"github.com/runwisp/runwisp/internal/storage"
)

var (
	ErrTaskNotFound       = errors.New("task not found")
	ErrAPIDisabled        = errors.New("API triggering disabled for this task")
	ErrRunNotFound        = errors.New("run not found")
	ErrNotRunning         = errors.New("run is not currently running")
	ErrServiceNotRunnable = errors.New("services cannot be triggered; use the restart endpoint")
	ErrNotAService        = errors.New("task is not a service")
)

type runService struct {
	db          storage.RunRepository
	taskManager runtime.TaskRunner
	tasks       map[string]*model.Task
	scheduler   runtime.ScheduleSource
	logDir      string
}

func newRunService(db storage.RunRepository, jm runtime.TaskRunner, tasks map[string]*model.Task, sched runtime.ScheduleSource, logDir string) *runService {
	return &runService{db: db, taskManager: jm, tasks: tasks, scheduler: sched, logDir: logDir}
}

func (s *runService) ListTasks() []model.TaskResponse {
	tasks := make([]model.TaskResponse, 0, len(s.tasks))
	for _, task := range s.tasks {
		tr := model.TaskResponse{Task: *task}
		if task.Cron != "" && s.scheduler != nil {
			tr.NextRunAt = s.scheduler.GetNextRun(task.Name)
		}
		tasks = append(tasks, tr)
	}
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

func (s *runService) ListRuns(taskName string, p PaginationParams) (*RunsResponseBody, error) {
	filterTaskName := taskName
	if filterTaskName == "" {
		filterTaskName = p.TaskName
	}
	runs, err := s.db.QueryRuns(filterTaskName, p.Limit, p.Offset, p.Status, p.SortField, p.SortDirection, p.SearchQuery)
	if err != nil {
		return nil, err
	}

	total, err := s.db.CountRunsFiltered(p.Status, filterTaskName, p.SearchQuery)
	if err != nil {
		return nil, err
	}

	return &RunsResponseBody{Runs: runs, Total: total}, nil
}

func (s *runService) GetRun(runID string) (*model.Run, error) {
	run, err := s.db.GetRun(runID)
	if err != nil {
		return nil, mapNotFound(err)
	}
	return run, nil
}

func (s *runService) TriggerRun(taskName string) (*model.Run, error) {
	task, exists := s.tasks[taskName]
	if !exists {
		return nil, ErrTaskNotFound
	}
	if task.Kind.IsService() {
		return nil, ErrServiceNotRunnable
	}
	if !task.APITrigger {
		return nil, ErrAPIDisabled
	}
	return s.taskManager.TriggerRun(taskName, model.TriggeredByAPI)
}

func (s *runService) RestartService(taskName string) error {
	task, exists := s.tasks[taskName]
	if !exists {
		return ErrTaskNotFound
	}
	if !task.Kind.IsService() {
		return ErrNotAService
	}
	return s.taskManager.RestartServiceInstances(taskName)
}

func (s *runService) StopService(taskName string) error {
	task, exists := s.tasks[taskName]
	if !exists {
		return ErrTaskNotFound
	}
	if !task.Kind.IsService() {
		return ErrNotAService
	}
	return s.taskManager.StopService(taskName)
}

func (s *runService) DeleteRun(runID string) error {
	run, err := s.db.GetRun(runID)
	if err != nil {
		return mapNotFound(err)
	}
	if err := s.db.DeleteRun(runID); err != nil {
		return err
	}
	logPath := logutil.ResolveRunLogPath(s.logDir, run.TaskName, run.ID, run.CreatedAt)
	logutil.RemoveLogFiles(logPath)
	logutil.RemoveEmptyParents(logPath, s.logDir)
	return nil
}

func (s *runService) StopRun(runID string) error {
	run, err := s.db.GetRun(runID)
	if err != nil {
		return mapNotFound(err)
	}
	if run.Status != model.PhaseRunning {
		return ErrNotRunning
	}
	return s.taskManager.TerminateRun(runID)
}
