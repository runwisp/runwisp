// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package station

import (
	"errors"
	"fmt"

	"github.com/runwisp/runwisp/apps/runwisp/internal/generated/protocol"
)

// StationError is a command failure the station gets back as the result's
// error: Code and Retryable go on the wire as they are.
type StationError struct {
	Code      protocol.ErrorCode
	Message   string
	Retryable bool
	Err       error
}

func (e *StationError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
}

func (e *StationError) Unwrap() error {
	return e.Err
}

// newError builds a StationError. Only internal failures are retryable by
// default; a caller that knows better sets Retryable itself.
func newError(code protocol.ErrorCode, format string, args ...any) *StationError {
	return &StationError{Code: code, Message: fmt.Sprintf(format, args...), Retryable: code == protocol.ErrorCodeInternal || code == protocol.ErrorCodeBusy}
}

// errorInfo turns a handler error into the result's error. An error that
// isn't a StationError is an internal, retryable failure.
func errorInfo(err error) *protocol.ErrorInfo {
	if se, ok := errors.AsType[*StationError](err); ok {
		msg := se.Message
		if se.Err != nil {
			msg = fmt.Sprintf("%s: %v", se.Message, se.Err)
		}
		return &protocol.ErrorInfo{Code: se.Code, Message: msg, Retryable: se.Retryable}
	}
	return &protocol.ErrorInfo{Code: protocol.ErrorCodeInternal, Message: err.Error(), Retryable: true}
}
