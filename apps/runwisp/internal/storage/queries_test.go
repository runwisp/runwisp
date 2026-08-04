// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package storage

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSortColumn(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    SortColumn
		wantErr bool
	}{
		{"empty falls through to default", "", SortColumnDefault, false},
		{"created_at allowed", "createdAt", SortColumnCreatedAt, false},
		{"start_at allowed", "startAt", SortColumnStartAt, false},
		{"task_name allowed", "taskName", SortColumnTaskName, false},
		{"status allowed", "status", SortColumnStatus, false},
		{"exit_code allowed", "exitCode", SortColumnExitCode, false},
		{"duration allowed", "duration", SortColumnDuration, false},
		{"unknown column rejected", "garbage", SortColumnDefault, true},
		// Defence against SQL-injection via the sort key.
		{"injection rejected", "id; DROP TABLE runs", SortColumnDefault, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseSortColumn(tt.raw)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.raw)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseSortDirection(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    SortDirection
		wantErr bool
	}{
		{"empty becomes default", "", SortDirectionDefault, false},
		{"asc accepted", "asc", SortAsc, false},
		{"desc accepted", "desc", SortDesc, false},
		{"unknown rejected", "sideways", SortDirectionDefault, true},
		{"uppercase rejected (case-sensitive)", "ASC", SortDirectionDefault, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseSortDirection(tt.raw)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.raw)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestQueryRunsSortKey(t *testing.T) {
	tests := []struct {
		name string
		col  SortColumn
		dir  SortDirection
		want string
	}{
		{"default column defaults to created_at desc", SortColumnDefault, SortDirectionDefault, "createdAt_desc"},
		{"default column honors explicit asc", SortColumnDefault, SortAsc, "createdAt_asc"},
		{"created_at asc", SortColumnCreatedAt, SortAsc, "createdAt_asc"},
		{"task_name desc", SortColumnTaskName, SortDesc, "taskName_desc"},
		{"duration asc", SortColumnDuration, SortAsc, "duration_asc"},
		{"start_at desc", SortColumnStartAt, SortDesc, "startAt_desc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := queryRunsSortKey(tt.col, tt.dir)
			assert.Equal(t, tt.want, got)
		})
	}
}
