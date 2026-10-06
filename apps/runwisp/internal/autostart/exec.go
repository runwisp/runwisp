// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package autostart

import (
	"bytes"
	"context"
	"os/exec"
)

// Runner is the subprocess seam used by the installer. Production
// uses execRunner which shells out via os/exec; tests use FakeRunner
// so they never spawn real systemctl/loginctl/launchctl processes.
type Runner interface {
	// Run executes name with args. stdout/stderr are returned even
	// when err is non-nil (so the caller can surface diagnostics).
	Run(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error)
}

// execRunner is the production Runner.
type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}
