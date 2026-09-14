// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"reflect"
	"testing"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
)

// TestRunsQueryInputCoversRunFilter guards the RunFilter/RunsQueryInput
// unification: RunsQueryInput can't literally embed model.RunFilter (huma
// panics at startup on a pointer-typed query field, and RunFilter's
// CreatedAfter/CreatedBefore/ExitCodeMin/ExitCodeMax are pointers so the JSON
// body can omit them correctly), so the two structs are hand-kept in sync
// instead. This test is the drift guard: every RunFilter field must have a
// same-named field on RunsQueryInput (the wire type may legitimately differ —
// a pointer on one side, a string/time.Time on the other — only the name is
// checked), so a new RunFilter field can never silently be un-settable from a
// plain GET query string the way IsFailure once was.
func TestRunsQueryInputCoversRunFilter(t *testing.T) {
	queryFields := map[string]struct{}{}
	qt := reflect.TypeOf(RunsQueryInput{})
	for i := 0; i < qt.NumField(); i++ {
		queryFields[qt.Field(i).Name] = struct{}{}
	}

	ft := reflect.TypeOf(model.RunFilter{})
	for i := 0; i < ft.NumField(); i++ {
		name := ft.Field(i).Name
		_, ok := queryFields[name]
		assert.Truef(t, ok, "model.RunFilter.%s has no matching query field on RunsQueryInput", name)
	}
}
