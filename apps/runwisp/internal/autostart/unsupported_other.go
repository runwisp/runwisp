// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !linux && !darwin

package autostart

import (
	"context"
	"io"
)

// New returns an installer that rejects every operation on the
// current OS. The error includes a doc link so the operator knows
// where to look for a manual recipe.
func New(_ Deps) (Installer, error) {
	return &unsupportedInstaller{}, nil
}

type unsupportedInstaller struct{}

func (u *unsupportedInstaller) ComputePlan(_ context.Context, _ InstallOptions) (Plan, error) {
	return Plan{}, ErrUnsupported
}

func (u *unsupportedInstaller) Install(_ context.Context, _ InstallOptions, _ io.Writer) error {
	return ErrUnsupported
}

func (u *unsupportedInstaller) ComputeUninstallPlan(_ context.Context, _ UninstallOptions) (Plan, error) {
	return Plan{}, ErrUnsupported
}

func (u *unsupportedInstaller) Uninstall(_ context.Context, _ UninstallOptions, _ io.Writer) error {
	return ErrUnsupported
}

func (u *unsupportedInstaller) Status(_ context.Context, _ InstallOptions) (Status, error) {
	return Status{}, ErrUnsupported
}

func (u *unsupportedInstaller) Stop(_ context.Context, _ InstallOptions) error {
	return ErrUnsupported
}

func (u *unsupportedInstaller) Restart(_ context.Context, _ InstallOptions) error {
	return ErrUnsupported
}

// Render is the --print contract; on unsupported OSes it just errors.
func (u *unsupportedInstaller) Render(_ InstallOptions) ([]byte, error) {
	return nil, ErrUnsupported
}

// CronStatus reports "nothing to take over" rather than ErrUnsupported: the
// caller uses it to decide whether to ask a question, and the honest answer
// on an OS with no installer is that there is no cron unit RunWisp manages.
func (u *unsupportedInstaller) CronStatus(_ context.Context) (string, bool, error) {
	return "", false, nil
}

// ScopeCandidates implements the per-OS half of DetectScope. Neither scope
// exists on an OS without an installer.
func ScopeCandidates(_ Deps) (systemPath, userPath string) {
	return "", ""
}
