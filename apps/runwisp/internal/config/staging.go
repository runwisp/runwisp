// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import "path/filepath"

// This file describes RunWisp's two-tier config layout: a root runwisp.toml the
// operator owns and keeps in git, plus a machine-owned staging file that
// `runwisp import` writes and `runwisp promote` graduates entries out of. The
// loader reads it (to derive Task.Source); internal/configedit writes it. Both
// sides agree on the paths here rather than each spelling them out.

// ImportedStagingBase is the staging file's basename. Tasks loaded from it are
// marked model.SourceStaged.
const ImportedStagingBase = "imported.toml"

// ImportedStagingSubdir is the machine-managed include dir the staging file
// lives in, relative to the root config, named after cron.d.
const ImportedStagingSubdir = "runwisp.d"

// StagingIncludeGlob is the include pattern `runwisp import` wires into the
// root config.
const StagingIncludeGlob = ImportedStagingSubdir + "/*.toml"

// StagingRelPath is the staging file's path relative to the root config's
// directory — the way the wired include line spells it, and the way the CLI names
// it in output. StagingFilePath is the absolute form used for comparisons.
func StagingRelPath() string {
	return filepath.Join(ImportedStagingSubdir, ImportedStagingBase)
}

// StagingFilePath returns the absolute path of the machine-owned staging file
// (runwisp.d/imported.toml) relative to the given root config directory. It is the
// single source of truth for where imports land and how provenance is derived,
// shared by the loader, the importer, and `promote`.
func StagingFilePath(rootDir string) string {
	p := filepath.Join(rootDir, ImportedStagingSubdir, ImportedStagingBase)
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}
