// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"testing"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
)

func TestBuildSelectorWhere_ExplicitIDs(t *testing.T) {
	sel := model.RunSelector{IDs: []string{"a", "b", "c"}}
	clause, args := buildSelectorWhere(sel)
	assert.Equal(t, "id IN (?,?,?)", clause)
	assert.Equal(t, []any{"a", "b", "c"}, args)
}

func TestBuildSelectorWhere_ExplicitEmptyIsNoMatch(t *testing.T) {
	// A misuse (empty ID list in explicit mode) must NEVER expand to a
	// full-table update — emit the always-false sentinel as a safety net.
	sel := model.RunSelector{IDs: nil}
	clause, args := buildSelectorWhere(sel)
	assert.Equal(t, "(1=0)", clause)
	assert.Nil(t, args)
}

func TestBuildSelectorWhere_MatchAllNoFilter(t *testing.T) {
	sel := model.RunSelector{MatchAll: true}
	clause, args := buildSelectorWhere(sel)
	assert.Equal(t, "(1=1)", clause)
	assert.Nil(t, args)
}

func TestBuildSelectorWhere_MatchAllWithTaskName(t *testing.T) {
	sel := model.RunSelector{
		MatchAll: true,
		Filter:   model.RunFilter{TaskName: "task1"},
	}
	clause, args := buildSelectorWhere(sel)
	assert.Equal(t, "(task_name = ?)", clause)
	assert.Equal(t, []any{"task1"}, args)
}

func TestBuildSelectorWhere_MatchAllWithExceptIDs(t *testing.T) {
	sel := model.RunSelector{
		MatchAll:  true,
		Filter:    model.RunFilter{TaskName: "task1"},
		ExceptIDs: []string{"x", "y"},
	}
	clause, args := buildSelectorWhere(sel)
	assert.Equal(t, "(task_name = ? AND id NOT IN (?,?))", clause)
	assert.Equal(t, []any{"task1", "x", "y"}, args)
}

func TestRunSelector_Validate(t *testing.T) {
	// MatchAll with IDs is a misuse — exactly one mode must be active.
	err := model.RunSelector{MatchAll: true, IDs: []string{"a"}}.Validate()
	assert.Error(t, err)

	// Explicit mode with empty IDs is rejected.
	err = model.RunSelector{MatchAll: false}.Validate()
	assert.Error(t, err)

	// Valid explicit selector.
	err = model.RunSelector{IDs: []string{"a"}}.Validate()
	assert.NoError(t, err)

	// Valid MatchAll selector.
	err = model.RunSelector{MatchAll: true}.Validate()
	assert.NoError(t, err)
}
