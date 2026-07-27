// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package execlist

import "fmt"

// instanceLabel formats a run's display label. A service configured with more
// than one instance gets a 1-based suffix (taskName#1, taskName#2, …) on every
// one of its runs; a single-instance task (or any non-service) shows the bare
// name. instanceIndex is the stored 0-based slot; instanceCount is the task's
// currently configured instance count (≤1 means "no suffix").
func instanceLabel(taskName string, instanceIndex, instanceCount int) string {
	if instanceCount > 1 {
		return fmt.Sprintf("%s#%d", taskName, instanceIndex+1)
	}
	return taskName
}
