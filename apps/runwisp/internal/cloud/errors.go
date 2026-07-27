// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package cloud

import (
	"errors"
	"fmt"
)

type CloudErrorKind string

const (
	CloudErrorKindAuth       CloudErrorKind = "AUTH_ERROR"
	CloudErrorKindValidation CloudErrorKind = "VALIDATION_ERROR"
	CloudErrorKindConflict   CloudErrorKind = "CONFLICT_ERROR"
	CloudErrorKindTransient  CloudErrorKind = "TRANSIENT_ERROR"
	// CloudErrorKindUnknownExecution signals that the daemon has no record of
	// the referenced execution (e.g. a stop for an execution it never ran).
	// The cloud reconciles such executions as abandoned.
	CloudErrorKindUnknownExecution CloudErrorKind = "UNKNOWN_EXECUTION"
)

type CloudError struct {
	Kind    CloudErrorKind
	Message string
	Err     error
}

func (e *CloudError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("%s: %s", e.Kind, e.Message)
	}
	return fmt.Sprintf("%s: %s: %v", e.Kind, e.Message, e.Err)
}

func (e *CloudError) Unwrap() error {
	return e.Err
}

func isHardAuthError(err error) bool {
	var cloudErr *CloudError
	if !errors.As(err, &cloudErr) {
		return false
	}
	return cloudErr.Kind == CloudErrorKindAuth
}

func classifyErrorKind(err error) CloudErrorKind {
	var cloudErr *CloudError
	if errors.As(err, &cloudErr) {
		return cloudErr.Kind
	}
	return CloudErrorKindTransient
}
