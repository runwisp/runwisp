// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package configedit

import (
	"fmt"
	"os"

	"github.com/runwisp/runwisp/internal/config"
)

// Promotion is the second half of the import story. `import` stages jobs in the
// machine-owned runwisp.d/imported.toml; promoting one moves its block into the
// operator's own runwisp.toml, where they own it outright — no more `staged`
// marker, no more risk of a re-import touching it.
//
// It is a move, not a conversion: the bytes that leave the staging file are the
// bytes that arrive in the root (see block.go). Nothing about what the daemon
// runs changes, which is why `promote` is safe to run against a live daemon.

// UnknownEntryError reports a requested name that the config doesn't define at
// all.
type UnknownEntryError struct{ Name string }

func (e *UnknownEntryError) Error() string { return fmt.Sprintf("no task named %q", e.Name) }

// NotStagedError reports a requested name that is already native: it lives in a
// file the operator maintains, so there is nothing to promote. File is the
// config file that defines it, or "" when the entry is generated from a compose
// project and has no block of its own.
type NotStagedError struct {
	Name string
	File string
}

func (e *NotStagedError) Error() string {
	if e.File == "" {
		return fmt.Sprintf("%q is not staged", e.Name)
	}
	return fmt.Sprintf("%q is not staged; it is defined in %s", e.Name, e.File)
}

// StagedNames returns the entries whose definitions live in the staging file, in
// config order. It is the set `promote --all` acts on and the set the CLI lists
// when nudging the operator.
func StagedNames(cfg *config.Config, layout Layout) []string {
	var names []string
	for i := range cfg.Tasks {
		name := cfg.Tasks[i].Name
		if cfg.OriginFile(name) == layout.StagingPath {
			names = append(names, name)
		}
	}
	return names
}

// Select resolves a promote request against the loaded config: all=true takes
// every staged entry, otherwise each requested name is checked and returned in
// the order the operator gave. Nothing is read from or written to disk here —
// this is the policy half, so the CLI can phrase a refusal before any file is
// touched.
//
// It refuses an unknown name (*UnknownEntryError) and a name that is already
// native (*NotStagedError) rather than silently skipping it: the operator named
// something specific and deserves to hear why it didn't happen.
func Select(cfg *config.Config, layout Layout, names []string, all bool) ([]string, error) {
	if all {
		return StagedNames(cfg, layout), nil
	}

	staged := make(map[string]struct{}, len(cfg.Tasks))
	for _, name := range StagedNames(cfg, layout) {
		staged[name] = struct{}{}
	}

	out := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if _, ok := staged[name]; !ok {
			return nil, notPromotable(cfg, name)
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out, nil
}

// notPromotable classifies why a name can't be promoted: it either isn't in the
// config at all, or it is but lives somewhere the operator already owns.
func notPromotable(cfg *config.Config, name string) error {
	for i := range cfg.Tasks {
		if cfg.Tasks[i].Name == name {
			return &NotStagedError{Name: name, File: cfg.OriginFile(name)}
		}
	}
	return &UnknownEntryError{Name: name}
}

// PromoteRequest describes one promotion: which layout, and which staged entries
// to move. Names is expected to have come from Select.
type PromoteRequest struct {
	Layout Layout
	Names  []string
}

// PromoteResult reports what moved.
type PromoteResult struct {
	// Promoted are the blocks that were appended to the root config, in the order
	// they appeared in the staging file.
	Promoted []Block
	// StagingRemoved is true when the staging file held nothing else and was
	// deleted rather than left behind empty.
	StagingRemoved bool
}

// PreviewBlocks cuts the named entries out of the staging file in memory and
// returns the residue plus the blocks, without touching a thing on disk. Promote
// builds on it, so a `--dry-run` preview can't drift from the move it describes.
func PreviewBlocks(layout Layout, names []string) (remaining []byte, blocks []Block, err error) {
	staging, err := os.ReadFile(layout.StagingPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", layout.StagingPath, err)
	}
	return ExtractBlocks(staging, names)
}

// Promote moves the named entries out of the staging file and into the root
// config, as one transaction gated on the merged config still loading. On any
// failure both files are left exactly as they were.
//
// Unlike Stage there is no "the config was already broken" case to distinguish:
// Select needs a loaded config to decide what is staged, so by the time Promote
// is called the caller has already proven the config loads. A gate failure here
// therefore means this move broke it — a *ConflictError, and a bug worth a
// report rather than a shrug.
func Promote(req PromoteRequest) (PromoteResult, error) {
	var res PromoteResult
	if len(req.Names) == 0 {
		return res, nil
	}

	remaining, blocks, err := PreviewBlocks(req.Layout, req.Names)
	if err != nil {
		return res, err
	}
	root, err := os.ReadFile(req.Layout.RootPath)
	if err != nil {
		return res, fmt.Errorf("read %s: %w", req.Layout.RootPath, err)
	}
	res.Promoted = blocks
	res.StagingRemoved = !HasEntries(remaining)

	txn := New()
	txn.Write(req.Layout.RootPath, AppendBlocks(root, blocks), DefaultPerm)
	if res.StagingRemoved {
		txn.Remove(req.Layout.StagingPath)
	} else {
		txn.Write(req.Layout.StagingPath, remaining, DefaultPerm)
	}

	if err := txn.Apply(func() error {
		if _, err := config.Load(req.Layout.RootPath); err != nil {
			return &ConflictError{Err: err}
		}
		return nil
	}); err != nil {
		return PromoteResult{}, err
	}
	return res, nil
}
