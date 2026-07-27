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
