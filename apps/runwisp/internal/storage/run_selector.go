// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"strings"

	"github.com/runwisp/runwisp/internal/model"
)

// buildSelectorWhere turns a RunSelector into a parameterized WHERE fragment.
// The returned string never contains a leading WHERE — callers append it with
// "AND" between their other predicates (e.g. "deleted_at IS NULL").
//
// "(1=1)" is returned when MatchAll is true and the filter is empty; callers
// can append it unconditionally without worrying about the empty-WHERE case.
func buildSelectorWhere(sel model.RunSelector) (string, []any) {
	if sel.MatchAll {
		return buildMatchAllWhere(sel)
	}
	return buildIDListWhere(sel.IDs)
}

func buildMatchAllWhere(sel model.RunSelector) (string, []any) {
	var q queryBuilder
	applyStatusFilter(&q, sel.Filter.Status)
	if sel.Filter.TaskName != "" {
		q.add("task_name = ?", sel.Filter.TaskName)
	}
	applySearchFilter(&q, sel.Filter.Search)
	if len(sel.ExceptIDs) > 0 {
		placeholders, args := idPlaceholders(sel.ExceptIDs)
		q.add("id NOT IN ("+placeholders+")", args...)
	}
	if len(q.where) == 0 {
		return "(1=1)", nil
	}
	return "(" + strings.Join(q.where, " AND ") + ")", q.args
}

func buildIDListWhere(ids []string) (string, []any) {
	if len(ids) == 0 {
		// Callers must validate before reaching here; "(1=0)" prevents an
		// accidentally unbounded UPDATE if validation is skipped.
		return "(1=0)", nil
	}
	placeholders, args := idPlaceholders(ids)
	return "id IN (" + placeholders + ")", args
}

func idPlaceholders(ids []string) (string, []any) {
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return placeholders, args
}
