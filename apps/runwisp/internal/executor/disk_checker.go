// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package executor

import (
	"fmt"
	"os"

	"github.com/runwisp/runwisp/internal/config"
)

// DiskChecker validates that enough free disk space is available before execution.
type DiskChecker struct {
	logDir      string
	minFreeDisk int64
}

func NewDiskChecker(logDir string, minFreeDisk int64) *DiskChecker {
	return &DiskChecker{logDir: logDir, minFreeDisk: minFreeDisk}
}

func (d *DiskChecker) Check() error {
	if err := os.MkdirAll(d.logDir, 0755); err != nil {
		return fmt.Errorf("failed to create log directory: %w", err)
	}
	if d.minFreeDisk > 0 {
		if free := freeDiskSpace(d.logDir); free >= 0 && free < d.minFreeDisk {
			return fmt.Errorf(
				"insufficient disk space: %s free, minimum %s required",
				config.FormatByteSize(free), config.FormatByteSize(d.minFreeDisk))
		}
	}
	return nil
}
