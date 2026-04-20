// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package apiclient

import (
	"fmt"
	"net/url"
	"strconv"

	"github.com/runwisp/runwisp/internal/model"
)

// RunsResponse mirrors the server's paginated runs response.
type RunsResponse struct {
	Runs  []model.Run `json:"runs"`
	Total int64       `json:"total"`
}

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

// encodeRunsParams builds URL query values from RunsParams.
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

	var resp RunsResponse
	if err := c.doJSON("GET", path, nil, &resp); err != nil {
		return nil, 0, err
	}
	return resp.Runs, resp.Total, nil
}

// ListRunsByTask returns paginated runs for a specific task.
func (c *Client) ListRunsByTask(taskName string, params RunsParams) ([]model.Run, int64, error) {
	path := fmt.Sprintf("/api/tasks/%s/runs", taskName)
	if qs := encodeRunsParams(params).Encode(); qs != "" {
		path += "?" + qs
	}

	var resp RunsResponse
	if err := c.doJSON("GET", path, nil, &resp); err != nil {
		return nil, 0, err
	}
	return resp.Runs, resp.Total, nil
}

// TriggerRun triggers a new run for the specified task.
func (c *Client) TriggerRun(taskName string) (*model.Run, error) {
	var run model.Run
	if err := c.doJSON("POST", fmt.Sprintf("/api/tasks/%s/run", taskName), nil, &run); err != nil {
		return nil, err
	}
	return &run, nil
}

// StopRun sends a stop signal to a running execution.
func (c *Client) StopRun(taskName string, runID string) error {
	return c.doJSON("POST", fmt.Sprintf("/api/tasks/%s/runs/%s/stop", taskName, runID), nil, nil)
}

// GetRun fetches a single run by task name and run ID.
func (c *Client) GetRun(taskName string, runID string) (*model.Run, error) {
	var run model.Run
	if err := c.doJSON("GET", fmt.Sprintf("/api/tasks/%s/runs/%s", taskName, runID), nil, &run); err != nil {
		return nil, err
	}
	return &run, nil
}

// DeleteRun removes a run record.
func (c *Client) DeleteRun(taskName string, runID string) error {
	return c.doJSON("DELETE", fmt.Sprintf("/api/tasks/%s/runs/%s", taskName, runID), nil, nil)
}
