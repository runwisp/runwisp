// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package execlist

import "testing"

func TestInstanceLabel(t *testing.T) {
	tests := []struct {
		name          string
		taskName      string
		instanceIndex int
		instanceCount int
		want          string
	}{
		{"single instance no suffix", "queue-worker", 0, 1, "queue-worker"},
		{"non-service (count 0) no suffix", "backup", 0, 0, "backup"},
		{"multi slot 0 is #1", "queue-worker", 0, 3, "queue-worker#1"},
		{"multi slot 1 is #2", "queue-worker", 1, 3, "queue-worker#2"},
		{"multi slot 2 is #3", "queue-worker", 2, 3, "queue-worker#3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := instanceLabel(tt.taskName, tt.instanceIndex, tt.instanceCount)
			if got != tt.want {
				t.Fatalf("instanceLabel(%q, %d, %d) = %q, want %q",
					tt.taskName, tt.instanceIndex, tt.instanceCount, got, tt.want)
			}
		})
	}
}
