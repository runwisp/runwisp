// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package configedit

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/runwisp/runwisp/internal/config"
)

// Layout is RunWisp's two-tier config layout rooted at one runwisp.toml: the
// root file the operator owns and keeps in git, plus the machine-owned staging
// file that `import` writes and `promote` graduates entries out of.
type Layout struct {
	RootPath    string
	StagingPath string
}

// NewLayout derives the layout from the root config path.
func NewLayout(rootPath string) Layout {
	return Layout{
		RootPath:    rootPath,
		StagingPath: config.StagingFilePath(filepath.Dir(rootPath)),
	}
}

// RootOutcome records what Stage had to do to the root config, so the caller can
// report it accurately instead of guessing.
type RootOutcome int

const (
	// RootCreated: no config existed, so a fresh two-tier root was written.
	RootCreated RootOutcome = iota
	// RootWired: an existing config gained the runwisp.d include line.
	RootWired
	// RootAlreadyIncluded: an existing config already covered runwisp.d and was
	// left byte-identical.
	RootAlreadyIncluded
)

// StageRequest describes one write of the staging file.
type StageRequest struct {
	// Layout names the root and staging paths to write.
	Layout Layout
	// Staging is the full contents of the staging file. It is machine-owned, so
	// it is overwritten without prompting.
	Staging []byte
	// Validate gates the write on the merged config still loading. Set it false
	// when the generated content is already known not to validate (an unmappable
	// job that became a `# TODO`) — then the files are kept so the operator can
	// fix them in place, which is the whole point of emitting the TODO.
	Validate bool
}

// StageResult reports where the staging file landed and what happened to the
// root config.
type StageResult struct {
	StagingPath string
	Root        RootOutcome
}

// ConflictError reports that the write made a config that used to load stop
// loading — almost always a task name defined in both the import and the
// operator's own files. The transaction was rolled back; nothing was written.
type ConflictError struct{ Err error }

func (e *ConflictError) Error() string { return "config conflict: " + e.Err.Error() }
func (e *ConflictError) Unwrap() error { return e.Err }

// PreexistingError reports that the root config already failed to load *before*
// this write. The write was rolled back, but the operator's problem predates it
// — reporting it as a conflict with the incoming import would send them looking
// in the wrong place.
type PreexistingError struct{ Err error }

func (e *PreexistingError) Error() string { return "config already invalid: " + e.Err.Error() }
func (e *PreexistingError) Unwrap() error { return e.Err }

// Stage writes the staging file and makes sure the root config loads it: the
// root is created from the two-tier scaffold when absent, or surgically wired
// when it has no include list of its own. Both files go through one Txn, so a
// rejected write leaves the config exactly as it was rather than leaving staged
// tasks that load nowhere.
//
// It returns ErrIncludeNeedsManualWiring when the root declares its own
// [daemon].include that doesn't cover the staging dir, *ConflictError when the
// write broke a previously-loadable config, and *PreexistingError when the root
// already didn't load.
func Stage(req StageRequest) (StageResult, error) {
	res := StageResult{StagingPath: req.Layout.StagingPath}

	plan, err := planRoot(req.Layout.RootPath)
	if err != nil {
		return res, err
	}
	res.Root = plan.outcome

	txn := New()
	txn.Write(req.Layout.StagingPath, req.Staging, DefaultPerm)
	if plan.bytes != nil {
		txn.Write(req.Layout.RootPath, plan.bytes, DefaultPerm)
	}

	var gate func() error
	if req.Validate {
		gate = func() error {
			if _, err := config.Load(req.Layout.RootPath); err != nil {
				if plan.preLoadErr != nil {
					return &PreexistingError{Err: plan.preLoadErr}
				}
				return &ConflictError{Err: err}
			}
			return nil
		}
	}
	if err := txn.Apply(gate); err != nil {
		return res, err
	}
	return res, nil
}

// rootPlan is what the root config should end up as.
type rootPlan struct {
	// bytes are the new root contents, or nil when the root needs no change.
	bytes   []byte
	outcome RootOutcome
	// preLoadErr is the root's load error from *before* the write, so a rejected
	// write can blame the right party.
	preLoadErr error
}

// planRoot decides what the root config should end up as. It never touches disk
// beyond reading.
func planRoot(rootPath string) (rootPlan, error) {
	orig, err := os.ReadFile(rootPath)
	if errors.Is(err, os.ErrNotExist) {
		return rootPlan{bytes: []byte(config.TwoTierRootConfig()), outcome: RootCreated}, nil
	}
	if err != nil {
		return rootPlan{}, fmt.Errorf("read %s: %w", rootPath, err)
	}

	plan := rootPlan{}
	// Loading before writing is what lets a rejected write blame the right
	// party: a config that was already broken is not a conflict with the import.
	if _, loadErr := config.Load(rootPath); loadErr != nil {
		plan.preLoadErr = loadErr
	}

	wired, changed, err := EnsureStagingInclude(orig, filepath.Dir(rootPath))
	if err != nil {
		return rootPlan{}, err
	}
	if !changed {
		plan.outcome = RootAlreadyIncluded
		return plan, nil
	}
	plan.bytes, plan.outcome = wired, RootWired
	return plan, nil
}
