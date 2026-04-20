// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package runtime

import (
	"fmt"
	"sync"

	"github.com/charmbracelet/log"
	"github.com/robfig/cron/v3"
	"github.com/runwisp/runwisp/internal/model"
)

// ScheduleResult holds the outcome of scheduling tasks.
type ScheduleResult struct {
	Scheduled int
	Warnings  []string
}

// Scheduler wraps robfig/cron to trigger tasks on a schedule.
type Scheduler struct {
	cron        *cron.Cron
	taskManager TaskRunner
	tasks       map[string]*model.Task
	entryIDs    map[string]cron.EntryID
	mutex       sync.Mutex
	started     bool
}

func NewScheduler(taskManager TaskRunner, tasks map[string]*model.Task) *Scheduler {
	return &Scheduler{
		cron:        cron.New(),
		taskManager: taskManager,
		tasks:       tasks,
		entryIDs:    make(map[string]cron.EntryID),
	}
}

func (scheduler *Scheduler) Start() (ScheduleResult, error) {
	scheduler.mutex.Lock()
	defer scheduler.mutex.Unlock()

	if scheduler.started {
		return ScheduleResult{}, nil
	}

	result := ScheduleResult{}
	for _, task := range scheduler.tasks {
		if task.Trigger.Cron == "" {
			continue
		}
		if err := scheduler.addTask(task); err != nil {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("failed to schedule %s: %v", task.Name, err))
			continue
		}
		result.Scheduled++
	}

	scheduler.cron.Start()
	scheduler.started = true
	return result, nil
}

func (scheduler *Scheduler) Stop() {
	scheduler.mutex.Lock()
	if !scheduler.started {
		scheduler.mutex.Unlock()
		return
	}

	ctx := scheduler.cron.Stop()
	scheduler.started = false
	scheduler.mutex.Unlock()

	<-ctx.Done()
}

func (scheduler *Scheduler) addTask(task *model.Task) error {
	taskName := task.Name
	entryID, err := scheduler.cron.AddFunc(task.Trigger.Cron, func() {
		log.Debug("Cron triggering task", "name", taskName)
		if _, err := scheduler.taskManager.TriggerRun(taskName, model.TriggeredByCron); err != nil {
			log.Error("Failed to trigger task", "name", taskName, "err", err)
		}
	})
	if err == nil {
		scheduler.entryIDs[taskName] = entryID
	}
	return err
}

// GetNextRun returns the next scheduled time for the task, if scheduled.
func (scheduler *Scheduler) GetNextRun(taskName string) *string {
	scheduler.mutex.Lock()
	defer scheduler.mutex.Unlock()

	entryID, ok := scheduler.entryIDs[taskName]
	if !ok {
		return nil
	}
	entry := scheduler.cron.Entry(entryID)
	next := entry.Next.Format("2006-01-02 15:04:05")
	return &next
}
