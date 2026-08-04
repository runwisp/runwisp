// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runtime/retry"
)

// jsonSchemaVersion identifies the shape of every --json document RunWisp's
// read commands emit. Agents branch on it to trust the fields below. It is a
// compatibility promise: once shipped, only additive changes are allowed —
// bump it if a field is ever removed or its meaning changes.
const jsonSchemaVersion = 1

// writeJSON marshals v as an indented JSON document with a trailing newline.
// It is the single stdout sink for every --json code path, so stdout stays a
// single valid JSON document and nothing else.
func writeJSON(w io.Writer, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if _, err := w.Write(b); err != nil {
		return err
	}
	_, err = io.WriteString(w, "\n")
	return err
}

// taskKindString normalizes model.KindTask ("") to the explicit "task" so the
// JSON always carries a concrete kind an agent can switch on.
func taskKindString(k model.TaskKind) string {
	if k.IsService() {
		return string(model.KindService)
	}
	return "task"
}

// --- status ---------------------------------------------------------------

// statusJSONDoc is the machine-readable form of `runwisp status`. It is the
// live snapshot: daemon reachability, a system summary, and every task with
// its last run. See statusTaskJSON / lastRunJSON.
type statusJSONDoc struct {
	SchemaVersion    int              `json:"schemaVersion"`
	Healthy          bool             `json:"healthy"`
	Error            string           `json:"error,omitempty"`
	Version          string           `json:"version,omitempty"`
	Port             int              `json:"port,omitempty"`
	ExternalURL      string           `json:"externalUrl,omitempty"`
	SchedulingActive bool             `json:"schedulingActive"`
	ConfigStale      bool             `json:"configStale"`
	ConfigWarnings   []string         `json:"configWarnings,omitempty"`
	ResolvedTimezone string           `json:"resolvedTimezone,omitempty"`
	TimezoneSource   string           `json:"timezoneSource,omitempty"`
	System           *statusSystem    `json:"system,omitempty"`
	Tasks            []statusTaskJSON `json:"tasks"`
}

// statusSystem is a curated subset of model.SystemStats — the identity and
// resource fields an operator or agent branches on, without the churny
// percentages that would make golden output non-deterministic.
type statusSystem struct {
	Version  string `json:"version"`
	Uptime   string `json:"uptime"`
	CPUCores int    `json:"cpuCores"`
	Host     string `json:"host"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Name     string `json:"name"`
	WorkDir  string `json:"workDir"`
}

type statusTaskJSON struct {
	Name       string       `json:"name"`
	Kind       string       `json:"kind"`
	Cron       string       `json:"cron,omitempty"`
	APITrigger bool         `json:"apiTrigger"`
	Source     string       `json:"source,omitempty"`
	SourceFile string       `json:"sourceFile,omitempty"`
	HeldBy     string       `json:"heldBy,omitempty"`
	NextRunAt  *string      `json:"nextRunAt,omitempty"`
	LastRun    *lastRunJSON `json:"lastRun"`
}

// lastRunJSON is the most recent run of a task. failed/missed are precomputed
// so an agent never has to know RunWisp's end-reason taxonomy: failed reuses
// retry.IsFailureReason, missed is the single ReasonMissed value.
type lastRunJSON struct {
	ID          string     `json:"id"`
	Status      string     `json:"status"`
	EndReason   *string    `json:"endReason,omitempty"`
	ExitCode    int        `json:"exitCode"`
	TriggeredBy string     `json:"triggeredBy"`
	StartAt     *time.Time `json:"startAt,omitempty"`
	EndAt       *time.Time `json:"endAt,omitempty"`
	DurationMS  *int64     `json:"durationMs,omitempty"`
	Failed      bool       `json:"failed"`
	Missed      bool       `json:"missed"`
}

func newStatusSystem(s *model.SystemStats) *statusSystem {
	if s == nil {
		return nil
	}
	return &statusSystem{
		Version:  s.Version,
		Uptime:   s.Uptime,
		CPUCores: s.CPUCores,
		Host:     s.Host,
		OS:       s.OS,
		Arch:     s.Arch,
		Name:     s.Name,
		WorkDir:  s.WorkDir,
	}
}

func newStatusTaskJSON(tr model.TaskResponse, last *model.Run) statusTaskJSON {
	st := statusTaskJSON{
		Name:       tr.Name,
		Kind:       taskKindString(tr.Kind),
		Cron:       tr.Cron,
		APITrigger: tr.APITrigger,
		Source:     string(tr.Source),
		SourceFile: tr.SourceFile,
		HeldBy:     string(tr.HeldBy),
		NextRunAt:  tr.NextRunAt,
	}
	if last != nil {
		lr := newLastRunJSON(last)
		st.LastRun = &lr
	}
	return st
}

func newLastRunJSON(r *model.Run) lastRunJSON {
	lr := lastRunJSON{
		ID:          r.ID,
		Status:      string(r.Status),
		ExitCode:    r.ExitCode,
		TriggeredBy: string(r.TriggeredBy),
		StartAt:     r.StartAt,
		EndAt:       r.EndAt,
	}
	if r.EndReason != nil {
		reason := string(*r.EndReason)
		lr.EndReason = &reason
		lr.Failed = retry.IsFailureReason(*r.EndReason)
		lr.Missed = *r.EndReason == model.ReasonMissed
	}
	if r.StartAt != nil && r.EndAt != nil {
		ms := r.EndAt.Sub(*r.StartAt).Milliseconds()
		lr.DurationMS = &ms
	}
	return lr
}

// --- exec ------------------------------------------------------------------

// execJSONDoc is the machine-readable outcome of `runwisp exec --json`: the one
// document written to stdout once the run reaches a terminal state (or is
// triggered, under --detach). Live log lines are diverted to stderr so stdout
// stays a single JSON document. failed is precomputed (retry.IsFailureReason)
// so an agent branches on one boolean without knowing the end-reason taxonomy.
type execJSONDoc struct {
	SchemaVersion int    `json:"schemaVersion"`
	Task          string `json:"task"`
	// Error is set only when the run never reached a terminal state (unknown
	// task, daemon unreachable, auth failure). The identity fields below are then
	// empty and omitted; on a real run they are always populated, so omitempty
	// never hides them for a genuine outcome.
	Error     string  `json:"error,omitempty"`
	RunID     string  `json:"runId,omitempty"`
	Status    string  `json:"status,omitempty"`
	EndReason *string `json:"endReason,omitempty"`
	// ExitCode is a pointer so the error document (no run) omits it rather than
	// emitting a misleading "exit_code": 0, while a genuine success still reports
	// an explicit 0.
	ExitCode    *int       `json:"exitCode,omitempty"`
	TriggeredBy string     `json:"triggeredBy,omitempty"`
	StartAt     *time.Time `json:"startAt,omitempty"`
	EndAt       *time.Time `json:"endAt,omitempty"`
	DurationMS  *int64     `json:"durationMs,omitempty"`
	Failed      bool       `json:"failed"`
}

// newExecErrorJSONDoc is the --json document emitted when a run never reaches a
// terminal state (unknown task, daemon unreachable, auth failure, …). It carries
// the task and the error text and nothing else, so an agent driving
// `exec --json` learns of the failure from stdout — matching validate/list/status,
// which also emit a JSON document on failure while still exiting non-zero.
func newExecErrorJSONDoc(taskName string, err error) execJSONDoc {
	return execJSONDoc{
		SchemaVersion: jsonSchemaVersion,
		Task:          taskName,
		Error:         err.Error(),
	}
}

func newExecJSONDoc(taskName string, r *model.Run) execJSONDoc {
	exitCode := r.ExitCode
	doc := execJSONDoc{
		SchemaVersion: jsonSchemaVersion,
		Task:          taskName,
		RunID:         r.ID,
		Status:        string(r.Status),
		ExitCode:      &exitCode,
		TriggeredBy:   string(r.TriggeredBy),
		StartAt:       r.StartAt,
		EndAt:         r.EndAt,
	}
	if r.EndReason != nil {
		reason := string(*r.EndReason)
		doc.EndReason = &reason
		doc.Failed = retry.IsFailureReason(*r.EndReason)
	}
	if r.StartAt != nil && r.EndAt != nil {
		ms := r.EndAt.Sub(*r.StartAt).Milliseconds()
		doc.DurationMS = &ms
	}
	return doc
}

// --- list ------------------------------------------------------------------

// listJSONDoc is the machine-readable form of `runwisp list`. Like the human
// table it is offline and config-only — no last-run state (that lives in
// `status --json`, which talks to the daemon).
type listJSONDoc struct {
	SchemaVersion int            `json:"schemaVersion"`
	Error         string         `json:"error,omitempty"`
	Tasks         []listTaskJSON `json:"tasks"`
}

type listTaskJSON struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Schedule  string `json:"schedule"`
	Instances int    `json:"instances,omitempty"`
	// MaxConcurrent is a pointer so it can be omitted for services, which have no
	// max_concurrent at all — their copy count is governed by `instances`, not an
	// overlap cap. Emitting a fabricated `1` here would contradict the config docs.
	MaxConcurrent *int   `json:"maxConcurrent,omitempty"`
	OnOverlap     string `json:"onOverlap"`
	APITrigger    bool   `json:"apiTrigger"`
	Source        string `json:"source,omitempty"`
	SourceFile    string `json:"sourceFile,omitempty"`
	HeldBy        string `json:"heldBy,omitempty"`
	Description   string `json:"description,omitempty"`
}

func newListTaskJSON(t model.Task) listTaskJSON {
	lt := listTaskJSON{
		Name:        t.Name,
		Kind:        taskKindString(t.Kind),
		Schedule:    t.Cron,
		OnOverlap:   string(t.OnOverlap),
		APITrigger:  t.APITrigger,
		Source:      string(t.Source),
		SourceFile:  t.SourceFile,
		HeldBy:      string(t.HeldBy),
		Description: t.Description,
	}
	if t.Kind.IsService() {
		lt.Instances = t.Instances
	} else {
		mc := t.MaxConcurrent
		lt.MaxConcurrent = &mc
	}
	return lt
}

// --- validate --------------------------------------------------------------

// validateJSONDoc is the machine-readable form of `runwisp validate`. valid is
// the single field an agent needs to branch on; errors/warnings carry the
// human messages. On failure the document is still emitted and the command
// still exits non-zero.
type validateJSONDoc struct {
	SchemaVersion  int           `json:"schemaVersion"`
	Valid          bool          `json:"valid"`
	ConfigPath     string        `json:"configPath"`
	Timezone       string        `json:"timezone,omitempty"`
	TimezoneSource string        `json:"timezoneSource,omitempty"`
	Tasks          int           `json:"tasks"`
	Services       int           `json:"services"`
	Warnings       []messageJSON `json:"warnings"`
	Errors         []messageJSON `json:"errors"`
}

// messageJSON is a validation error or advisory warning. For parse-time errors
// the config layer knows the source position, so key/line/column are populated
// (via config.LocatedError) to point an agent straight at the site; semantic
// errors and warnings carry the message only.
type messageJSON struct {
	Message string `json:"message"`
	Key     string `json:"key,omitempty"`
	Line    int    `json:"line,omitempty"`
	Column  int    `json:"column,omitempty"`
}

func messagesFromStrings(ss []string) []messageJSON {
	out := make([]messageJSON, 0, len(ss))
	for _, s := range ss {
		out = append(out, messageJSON{Message: s})
	}
	return out
}

// messageFromError builds a messageJSON from a config error, lifting the
// structured source location when the error is a config.LocatedError.
func messageFromError(err error) messageJSON {
	m := messageJSON{Message: err.Error()}
	var located *config.LocatedError
	if errors.As(err, &located) {
		m.Key = located.Key
		m.Line = located.Line
		m.Column = located.Column
	}
	return m
}

// messagesFromError expands a config error into one messageJSON per underlying
// problem: a joined error (several unknown keys, or several invalid tasks)
// yields one entry each with its own key/line/column; any other error yields a
// single entry. This is why `validate --json` reports every location, not just
// the first.
func messagesFromError(err error) []messageJSON {
	if multi, ok := err.(interface{ Unwrap() []error }); ok {
		if errs := multi.Unwrap(); len(errs) > 0 {
			out := make([]messageJSON, 0, len(errs))
			for _, e := range errs {
				out = append(out, messageFromError(e))
			}
			return out
		}
	}
	return []messageJSON{messageFromError(err)}
}
