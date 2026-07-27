// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package autostart

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"sync"
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

// NewRunner returns the production Runner.
func NewRunner() Runner { return execRunner{} }

// FakeRunner is a scriptable Runner for tests. Each registered call
// matches once (in registration order) and then is exhausted; an
// unregistered call returns an error so missing scripting is caught.
type FakeRunner struct {
	mu    sync.Mutex
	calls []fakeCall
	log   []FakeCallLog
}

type fakeCall struct {
	name   string
	args   []string
	stdout []byte
	stderr []byte
	err    error
}

// FakeCallLog records every call attempted against the FakeOSCmd.
type FakeCallLog struct {
	Name string
	Args []string
}

func NewFakeRunner() *FakeRunner { return &FakeRunner{} }

// Expect adds a scripted call that returns the given stdout/stderr/err.
func (f *FakeRunner) Expect(name string, args []string, stdout, stderr []byte, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeCall{name: name, args: args, stdout: stdout, stderr: stderr, err: err})
}

func (f *FakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log = append(f.log, FakeCallLog{Name: name, Args: append([]string(nil), args...)})
	for i, c := range f.calls {
		if c.name == name && argsEqual(c.args, args) {
			f.calls = append(f.calls[:i], f.calls[i+1:]...)
			return c.stdout, c.stderr, c.err
		}
	}
	return nil, nil, fmt.Errorf("FakeRunner: unexpected call %s %v", name, args)
}

// Log returns the recorded call sequence (a copy).
func (f *FakeRunner) Log() []FakeCallLog {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]FakeCallLog, len(f.log))
	copy(out, f.log)
	return out
}

// Remaining returns the count of scripted calls that were not consumed.
func (f *FakeRunner) Remaining() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func argsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
