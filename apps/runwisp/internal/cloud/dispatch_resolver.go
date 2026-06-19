// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package cloud

import (
	"fmt"
	"strings"
	"time"

	"github.com/runwisp/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/internal/model"
)

// resolveDispatchTask resolves a dispatch to a runnable task name. configBacked
// reports whether the task is one of this daemon's TOML-defined tasks (which may
// declare params): only those resolve InputValues. Inline ad-hoc executions
// declare no params, so their InputValues are ignored, not rejected.
func (h *InboundHandler) resolveDispatchTask(dispatch *protocol.Execution) (taskName string, configBacked bool, err error) {
	execDef, err := model.ParseExecutionDef(dispatch.Script)
	if err != nil {
		return "", false, &CloudError{
			Kind:    CloudErrorKindValidation,
			Message: fmt.Sprintf("failed to parse execution def: %v", err),
		}
	}

	// Reject execution types the daemon doesn't allow for cloud dispatch.
	status := h.availability.ForType(execDef.ExecType())
	if !status.Available {
		return "", false, &CloudError{
			Kind:    CloudErrorKindConflict,
			Message: fmt.Sprintf("execution type %q not available: %s", execDef.ExecType(), status.Reason),
		}
	}

	if cfg, ok := execDef.(*model.ConfigExecution); ok {
		// Config type means "resolve from this daemon's local tasks".
		if _, exists := h.taskManager.GetTask(cfg.TaskName); !exists {
			return "", false, &CloudError{Kind: CloudErrorKindConflict, Message: fmt.Sprintf("config task '%s' not found", cfg.TaskName)}
		}
		return cfg.TaskName, true, nil
	}

	task := buildDynamicCloudTask(dispatch, execDef)
	h.taskManager.UpsertTask(task)
	return task.Name, false, nil
}

func buildDynamicCloudTask(dispatch *protocol.Execution, execDef model.ExecutionDef) *model.Task {
	taskName := sanitizeCloudTaskName(dispatch.TaskID)
	if taskName == "" {
		taskName = sanitizeCloudTaskName(dispatch.TaskName)
	}
	if taskName == "" {
		taskName = "cloud-inline"
	}

	task := &model.Task{
		Name:          taskName,
		ExecutionDef:  execDef,
		MaxConcurrent: 1,
		OnOverlap:     model.PolicyQueue,
	}

	if dispatch.Timeout > 0 {
		task.Timeout = time.Duration(dispatch.Timeout) * time.Millisecond
	}

	applyCloudTaskConfig(task, dispatch.TaskConfig)

	return task
}

// applyCloudTaskConfig overlays the optional per-run execution knobs the cloud
// control plane carries in ExecutionPayload.taskConfig onto the dynamically
// built task.
func applyCloudTaskConfig(task *model.Task, cfg *protocol.ExecutionTaskConfig) {
	if cfg == nil {
		return
	}
	logOnFull := ""
	if cfg.LogOnFull != nil {
		logOnFull, _ = cfg.LogOnFull.Value().(string)
	}
	applyTaskConfigKnobs(task, cfg.Env, cfg.GracefulStop, cfg.LogMaxSize, logOnFull)
}

// applyTaskConfigKnobs overlays the optional per-run/per-process execution knobs
// (env, graceful-stop, log limits) onto task. Shared by the execution- and
// service-dispatch paths, whose generated taskConfig messages are structurally
// identical but distinctly typed (ExecutionTaskConfig / ServiceTaskConfig). Each
// field is honored only when set; an omitted/zero value leaves the daemon
// default in place. gracefulStop arrives in milliseconds and is stored as a
// Duration.
func applyTaskConfigKnobs(task *model.Task, env map[string]string, gracefulStop, logMaxSize int, logOnFull string) {
	if len(env) > 0 {
		task.Env = env
	}
	if gracefulStop > 0 {
		task.GracefulStop = time.Duration(gracefulStop) * time.Millisecond
	}
	if logMaxSize > 0 {
		task.LogMaxSize = int64(logMaxSize)
	}
	if logOnFull != "" {
		task.LogOnFull = logOnFull
	}
}

func sanitizeCloudTaskName(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	return strings.Trim("cloud-"+model.SanitizeTaskName(raw), "-_")
}
