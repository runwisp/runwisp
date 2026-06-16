// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0
//
// Code generated from packages/asyncapi/asyncapi.yaml; DO NOT EDIT.

package protocol

import (
	"encoding/json"
)

type HitsItemStream uint

const (
	HitsItemStreamStdout HitsItemStream = iota
	HitsItemStreamStderr
	HitsItemStreamSystem
)

// Value returns the value of the enum.
func (op HitsItemStream) Value() any {
	if op >= HitsItemStream(len(HitsItemStreamValues)) {
		return nil
	}
	return HitsItemStreamValues[op]
}

var HitsItemStreamValues = []any{"stdout", "stderr", "system"}
var ValuesToHitsItemStream = map[any]HitsItemStream{
	HitsItemStreamValues[HitsItemStreamStdout]: HitsItemStreamStdout,
	HitsItemStreamValues[HitsItemStreamStderr]: HitsItemStreamStderr,
	HitsItemStreamValues[HitsItemStreamSystem]: HitsItemStreamSystem,
}

func (op *HitsItemStream) UnmarshalJSON(raw []byte) error {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	*op = ValuesToHitsItemStream[v]
	return nil
}

func (op HitsItemStream) MarshalJSON() ([]byte, error) {
	return json.Marshal(op.Value())
}
