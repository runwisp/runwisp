// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package cloud

import (
	"fmt"
	"strings"

	"github.com/runwisp/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/internal/model"
)

func (h *InboundHandler) resolveDispatchTask(dispatch *protocol.Execution) (string, error) {
	execDef, err := model.ParseExecutionDef(dispatch.Script)
	if err != nil {
		return "", &CloudError{
			Kind:    CloudErrorKindValidation,
			Message: fmt.Sprintf("failed to parse execution def: %v", err),
		}
	}

	// Reject execution types the daemon doesn't allow for cloud dispatch.
	status := h.availability.ForType(execDef.ExecType())
	if !status.Available {
		return "", &CloudError{
			Kind:    CloudErrorKindConflict,
			Message: fmt.Sprintf("execution type %q not available: %s", execDef.ExecType(), status.Reason),
		}
	}

	if cfg, ok := execDef.(*model.ConfigExecution); ok {
		// Config type means "resolve from this daemon's local tasks".
		if _, exists := h.taskManager.GetTask(cfg.TaskName); !exists {
			return "", &CloudError{Kind: CloudErrorKindConflict, Message: fmt.Sprintf("config task '%s' not found", cfg.TaskName)}
		}
		return cfg.TaskName, nil
	}

	task := buildDynamicCloudTask(dispatch, execDef)
	h.taskManager.UpsertTask(task)
	return task.Name, nil
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
		Name:         taskName,
		ExecutionDef: execDef,
		Parallelism:  1,
		OnOverlap:    model.PolicyQueue,
	}

	if dispatch.Timeout > 0 {
		task.Timeout = fmt.Sprintf("%dms", dispatch.Timeout)
	}

	return task
}

func sanitizeCloudTaskName(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	return strings.Trim("cloud-"+model.SanitizeTaskName(raw), "-_")
}
