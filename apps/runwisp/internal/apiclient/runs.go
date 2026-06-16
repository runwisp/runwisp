// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package apiclient

import (
	"fmt"
	"net/url"
	"strconv"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/server"
)

// RunsParams holds query parameters for listing runs.
type RunsParams struct {
	Limit         int
	Offset        int
	Status        string
	TaskName      string
	SortField     string
	SortDirection string
	Search        string
}

func (c *Client) ListTasks() ([]model.TaskResponse, error) {
	var tasks []model.TaskResponse
	if err := c.doJSON("GET", "/api/tasks", nil, &tasks); err != nil {
		return nil, err
	}
	return tasks, nil
}

func encodeRunsParams(params RunsParams) url.Values {
	q := url.Values{}
	if params.Limit > 0 {
		q.Set("limit", strconv.Itoa(params.Limit))
	}
	if params.Offset > 0 {
		q.Set("offset", strconv.Itoa(params.Offset))
	}
	if params.Status != "" {
		q.Set("status", params.Status)
	}
	if params.TaskName != "" {
		q.Set("task_name", params.TaskName)
	}
	if params.SortField != "" {
		q.Set("sort_field", params.SortField)
	}
	if params.SortDirection != "" {
		q.Set("sort_direction", params.SortDirection)
	}
	if params.Search != "" {
		q.Set("search", params.Search)
	}
	return q
}

func (c *Client) ListRuns(params RunsParams) ([]model.Run, int64, error) {
	path := "/api/runs"
	if qs := encodeRunsParams(params).Encode(); qs != "" {
		path += "?" + qs
	}

	var resp server.RunsResponseBody
	if err := c.doJSON("GET", path, nil, &resp); err != nil {
		return nil, 0, err
	}
	return resp.Runs, resp.Total, nil
}

func (c *Client) ListRunsByTask(taskName string, params RunsParams) ([]model.Run, int64, error) {
	path := fmt.Sprintf("/api/tasks/%s/runs", taskName)
	if qs := encodeRunsParams(params).Encode(); qs != "" {
		path += "?" + qs
	}

	var resp server.RunsResponseBody
	if err := c.doJSON("GET", path, nil, &resp); err != nil {
		return nil, 0, err
	}
	return resp.Runs, resp.Total, nil
}

// TriggerRun starts a new run of a task, optionally supplying values for the
// task's declared parameters. Pass nil params for a bare trigger. A per-key nil
// pointer omits that parameter (overriding its default); an empty string passes
// an empty value.
func (c *Client) TriggerRun(taskName string, params map[string]*string) (*model.Run, error) {
	var body any
	if len(params) > 0 {
		body = map[string]any{"params": params}
	}
	var run model.Run
	if err := c.doJSON("POST", fmt.Sprintf("/api/tasks/%s/run", taskName), body, &run); err != nil {
		return nil, err
	}
	return &run, nil
}

// RestartService restarts every instance of a service task.
func (c *Client) RestartService(taskName string) error {
	return c.doJSON("POST", fmt.Sprintf("/api/tasks/%s/restart", taskName), nil, nil)
}

// StopService stops a service for the lifetime of the daemon.
func (c *Client) StopService(taskName string) error {
	return c.doJSON("POST", fmt.Sprintf("/api/tasks/%s/stop", taskName), nil, nil)
}

// StopRun sends a stop signal to a running execution.
func (c *Client) StopRun(taskName, runID string) error {
	return c.doJSON("POST", fmt.Sprintf("/api/tasks/%s/runs/%s/stop", taskName, runID), nil, nil)
}

func (c *Client) GetRun(taskName, runID string) (*model.Run, error) {
	var run model.Run
	if err := c.doJSON("GET", fmt.Sprintf("/api/tasks/%s/runs/%s", taskName, runID), nil, &run); err != nil {
		return nil, err
	}
	return &run, nil
}

func (c *Client) DeleteRun(taskName, runID string) error {
	return c.doJSON("DELETE", fmt.Sprintf("/api/tasks/%s/runs/%s", taskName, runID), nil, nil)
}
