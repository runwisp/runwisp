// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package configedit

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/runwisp/runwisp/apps/runwisp/internal/config"
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
	// Staging is what this import contributes to the staging file. Entries
	// already staged under other names are kept; one staged under a name this
	// import also uses is replaced (see mergeStaging).
	Staging []byte
}

// StageResult reports where the staging file landed and what happened to the
// root config.
type StageResult struct {
	StagingPath string
	Root        RootOutcome
	// PreLoadErr is the root config's load error from *before* this write, when
	// there was one. A real Stage never returns a result alongside one (the gate
	// turns it into a *PreexistingError), so this matters to PlanStage, where it's
	// the failure a real run would stop on.
	PreLoadErr error
	// Kept are the staged entries from earlier imports this one left in place;
	// Replaced are the staged entries it overwrote because it uses their names.
	Kept, Replaced []string
}

// ConflictError reports that the write made a config that used to load stop
// loading — almost always a task name defined in both the import and the
// operator's own files. The transaction was rolled back; nothing was written.
type ConflictError struct{ Err error }

func (e *ConflictError) Error() string { return "config conflict: " + e.Err.Error() }
func (e *ConflictError) Unwrap() error { return e.Err }

// loadGate is the Txn.Apply gate every config edit shares: the edited config
// must still load, or the write is a *ConflictError and rolls back.
func loadGate(path string) func() error {
	return func() error {
		if _, err := config.Load(path); err != nil {
			return &ConflictError{Err: err}
		}
		return nil
	}
}

// PreexistingError reports that the root config already failed to load *before*
// this write. The write was rolled back, but the operator's problem predates it
// — reporting it as a conflict with the incoming import would send them looking
// in the wrong place.
type PreexistingError struct{ Err error }

func (e *PreexistingError) Error() string { return "config already invalid: " + e.Err.Error() }
func (e *PreexistingError) Unwrap() error { return e.Err }

// Stage merges the import into the staging file (see mergeStaging) and makes
// sure the root config loads it: the root is created from the two-tier
// scaffold when absent, or surgically wired when it has no include list of its
// own. Both files go through one Txn, so a
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
	res.PreLoadErr = plan.preLoadErr
	merged, err := mergeStaging(req.Layout.StagingPath, req.Staging, &res)
	if err != nil {
		return res, err
	}

	txn := New()
	txn.Write(req.Layout.StagingPath, merged, DefaultPerm)
	if plan.bytes != nil {
		txn.Write(req.Layout.RootPath, plan.bytes, DefaultPerm)
	}

	err = txn.Apply(func() error {
		err := loadGate(req.Layout.RootPath)()
		if err != nil && plan.preLoadErr != nil {
			return &PreexistingError{Err: plan.preLoadErr}
		}
		return err
	})
	return res, err
}

// PlanStage reports what Stage would do to the root config without touching
// disk beyond reading. It shares Stage's planning step rather than
// reimplementing it, so a `--dry-run` account can't drift from what the real
// write goes on to do.
//
// What it can't answer is whether the *merged* config loads: proving that means
// writing both files and calling config.Load on the result. It does surface the
// root's current load error in PreLoadErr, which is the one failure a real run
// would refuse on that's knowable in advance.
func PlanStage(layout Layout, staging []byte) (StageResult, error) {
	res := StageResult{StagingPath: layout.StagingPath}
	plan, err := planRoot(layout.RootPath)
	if err != nil {
		return res, err
	}
	res.Root = plan.outcome
	res.PreLoadErr = plan.preLoadErr
	_, err = mergeStaging(layout.StagingPath, staging, &res)
	return res, err
}

// mergeStaging is the staging file an import leaves behind: the incoming
// content, followed by every entry already staged under a name the import
// doesn't use. Imports from different sources, or of a few units at a time,
// accumulate instead of each one deleting the last; re-importing a job under
// its staged name replaces it. It records both lists on res.
func mergeStaging(path string, incoming []byte, res *StageResult) ([]byte, error) {
	old, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return incoming, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	fresh := EntryNames(incoming)
	for _, name := range EntryNames(old) {
		if slices.Contains(fresh, name) {
			res.Replaced = append(res.Replaced, name)
		} else {
			res.Kept = append(res.Kept, name)
		}
	}
	if len(res.Kept) == 0 {
		return incoming, nil
	}
	_, blocks, err := ExtractBlocks(old, res.Kept)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return AppendBlocks(incoming, blocks), nil
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
		return rootPlan{bytes: []byte(config.TwoTierRootConfig), outcome: RootCreated}, nil
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
