// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package station

import (
	"errors"
	"fmt"
)

type StationErrorKind string

const (
	StationErrorKindAuth       StationErrorKind = "AUTH_ERROR"
	StationErrorKindValidation StationErrorKind = "VALIDATION_ERROR"
	StationErrorKindConflict   StationErrorKind = "CONFLICT_ERROR"
	StationErrorKindTransient  StationErrorKind = "TRANSIENT_ERROR"
	// StationErrorKindUnknownExecution signals that the daemon has no record of
	// the referenced execution (e.g. a stop for an execution it never ran).
	// The station reconciles such executions as abandoned.
	StationErrorKindUnknownExecution StationErrorKind = "UNKNOWN_EXECUTION"
)

type StationError struct {
	Kind    StationErrorKind
	Message string
	Err     error
}

func (e *StationError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("%s: %s", e.Kind, e.Message)
	}
	return fmt.Sprintf("%s: %s: %v", e.Kind, e.Message, e.Err)
}

func (e *StationError) Unwrap() error {
	return e.Err
}

func isHardAuthError(err error) bool {
	var stationErr *StationError
	if !errors.As(err, &stationErr) {
		return false
	}
	return stationErr.Kind == StationErrorKindAuth
}

func classifyErrorKind(err error) StationErrorKind {
	var stationErr *StationError
	if errors.As(err, &stationErr) {
		return stationErr.Kind
	}
	return StationErrorKindTransient
}
