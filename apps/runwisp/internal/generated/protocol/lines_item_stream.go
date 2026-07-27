// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later
//
// Code generated from packages/asyncapi/asyncapi.yaml; DO NOT EDIT.

package protocol

import (
	"encoding/json"
)

type LinesItemStream uint

const (
	LinesItemStreamStdout LinesItemStream = iota
	LinesItemStreamStderr
	LinesItemStreamSystem
)

// Value returns the value of the enum.
func (op LinesItemStream) Value() any {
	if op >= LinesItemStream(len(LinesItemStreamValues)) {
		return nil
	}
	return LinesItemStreamValues[op]
}

var LinesItemStreamValues = []any{"stdout", "stderr", "system"}
var ValuesToLinesItemStream = map[any]LinesItemStream{
	LinesItemStreamValues[LinesItemStreamStdout]: LinesItemStreamStdout,
	LinesItemStreamValues[LinesItemStreamStderr]: LinesItemStreamStderr,
	LinesItemStreamValues[LinesItemStreamSystem]: LinesItemStreamSystem,
}

func (op *LinesItemStream) UnmarshalJSON(raw []byte) error {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	*op = ValuesToLinesItemStream[v]
	return nil
}

func (op LinesItemStream) MarshalJSON() ([]byte, error) {
	return json.Marshal(op.Value())
}
