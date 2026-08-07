// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package configedit

import (
	"fmt"
	"os"

	"github.com/runwisp/runwisp/internal/config"
)

// Promotion is the second half of both migration stories. `import` stages jobs in
// the machine-owned runwisp.d/imported.toml and `[daemon] include_cron` reads them
// straight out of a crontab; promoting one puts its block in the operator's own
// runwisp.toml, where they own it outright — no more provenance marker, no more
// risk of a re-import touching it.
//
// It is a move, not a conversion: the bytes that leave the staging file are the
// bytes that arrive in the root (see block.go). Nothing about what the daemon
// runs changes, which is why `promote` is safe to run against a live daemon.
//
// A cron-sourced task is the one asymmetry. Its definition has no TOML bytes on
// disk to move — the crontab is the definition — so promoting one *copies* the
// block the live loader rendered (config.CronBlockTOML) into the root. The copy
// alone isn't safe: once provenance flips to native RunWisp can no longer hold
// the task for cron (markCronHold only holds a Source == SourceCron task), so a
// crontab line left in place would be fired by both a still-live cron daemon and
// RunWisp. So the source line is commented out in the same transaction —
// croncomment.go verifies it byte-for-byte against the file first and refuses the
// whole promotion, changing nothing, if it moved or changed since load. Cron
// ignores a '#' line, so the job stops firing from cron; the line stays visible
// (with a note pointing at the runwisp.toml it moved to) rather than vanishing.
//
// The crontab write is queued before the root write for a reason. There is no
// cross-file atomic primitive: within one Apply a gate or write failure rolls
// every touched file back to its pre-image (see Txn), but a hard kill between the
// two renames can't be caught. Commenting the crontab first means such a crash
// leaves the job commented-out (cron won't fire it) but not yet in root — a
// recoverable state the operator can see and finish by hand — rather than firing
// from both.

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

// PromotableNames returns the entries whose definitions RunWisp derived rather
// than the operator writing them — staged imports and cron-sourced jobs — in
// config order. It is the set `promote --all` acts on and the set the CLI lists
// when nudging the operator.
//
// It keys off Task.Source rather than comparing origin paths, so a new provenance
// becomes promotable by being stamped rather than by every caller learning to
// recognise one more kind of file.
func PromotableNames(cfg *config.Config) []string {
	var names []string
	for i := range cfg.Tasks {
		if cfg.Tasks[i].Source.Promotable() {
			names = append(names, cfg.Tasks[i].Name)
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
		return PromotableNames(cfg), nil
	}

	staged := make(map[string]struct{}, len(cfg.Tasks))
	for _, name := range PromotableNames(cfg) {
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
	// Config is the loaded config the names came from — required, since Select
	// needed one to decide what was promotable in the first place. Promote asks it
	// which file each name lives in and, for a cron-sourced one, for the block the
	// live loader rendered. Passing the config rather than a pre-built block map
	// keeps the caller from having to get that mapping right.
	Config *config.Config
}

// PromoteResult reports what moved.
type PromoteResult struct {
	// Promoted are the blocks that were appended to the root config, in the order
	// they appeared in the staging file.
	Promoted []Block
	// StagingRemoved is true when the staging file held nothing else and was
	// deleted rather than left behind empty.
	StagingRemoved bool
	// CronCommentOuts are the crontab lines commented out alongside a
	// cron-sourced promotion, one per promoted cron task.
	CronCommentOuts []CronCommentOut
}

// PreviewBlocks resolves what a promotion would append and what would be left in
// the staging file, without touching a thing on disk. Promote builds on it, so a
// `--dry-run` preview can't drift from the move it describes.
//
// blocks come back in the order the operator's names were resolved: staged ones cut
// out of the staging file, cron-sourced ones rendered from the config. remaining is
// the staging file's residue, and is nil when nothing staged was selected — a
// cron-only promotion must not rewrite a file it isn't touching.
func PreviewBlocks(layout Layout, names []string, cfg *config.Config) (remaining []byte, blocks []Block, err error) {
	stagedSel, cronSel := splitByProvenance(names, layout, cfg)

	for _, name := range cronSel {
		text, _ := cfg.CronBlockTOML(name)
		blocks = append(blocks, Block{Name: name, Table: "tasks", Text: text})
	}
	if len(stagedSel) == 0 {
		return nil, blocks, nil
	}

	staging, err := os.ReadFile(layout.StagingPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", layout.StagingPath, err)
	}
	remaining, staged, err := ExtractBlocks(staging, stagedSel)
	if err != nil {
		return nil, nil, err
	}
	return remaining, append(staged, blocks...), nil
}

// splitByProvenance sorts selected names into the ones whose bytes are moved out
// of the staging file and the ones copied from a cron source, each keeping the
// caller's order.
//
// A name is cron-sourced only if the config positively has a rendered block for
// it. Identifying the new case rather than everything-that-isn't-staged means a
// name the config can't classify falls through to the staging path, which is the
// behaviour that predates cron sources and the one whose failure mode is a clear
// "no such block in the staging file".
func splitByProvenance(names []string, layout Layout, cfg *config.Config) (staged, cron []string) {
	for _, name := range names {
		if cfg != nil {
			if _, ok := cfg.CronBlockTOML(name); ok {
				cron = append(cron, name)
				continue
			}
		}
		staged = append(staged, name)
	}
	return staged, cron
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

	_, cronNames := splitByProvenance(req.Names, req.Layout, req.Config)
	var cronEdits map[string][]byte
	if len(cronNames) > 0 {
		commentOuts, edits, err := PlanCronCommentOuts(cronNames, req.Config, req.Layout.RootPath)
		if err != nil {
			return res, err
		}
		res.CronCommentOuts = commentOuts
		cronEdits = edits
	}

	remaining, blocks, err := PreviewBlocks(req.Layout, req.Names, req.Config)
	if err != nil {
		return res, err
	}
	root, err := os.ReadFile(req.Layout.RootPath)
	if err != nil {
		return res, fmt.Errorf("read %s: %w", req.Layout.RootPath, err)
	}
	res.Promoted = blocks

	txn := New()
	// Crontab edits are queued — and so written — before the root config: see the
	// package doc for why that order is the deliberate answer to the one failure
	// mode that can't be gated away, a hard kill between the two renames.
	for file, data := range cronEdits {
		txn.WritePreservingOwner(file, data)
	}
	txn.Write(req.Layout.RootPath, AppendBlocks(root, blocks), DefaultPerm)
	// remaining is nil for a cron-only promotion: there is no staging file in play,
	// so neither rewriting nor deleting it is correct.
	if remaining != nil {
		res.StagingRemoved = !HasEntries(remaining)
		if res.StagingRemoved {
			txn.Remove(req.Layout.StagingPath)
		} else {
			txn.Write(req.Layout.StagingPath, remaining, DefaultPerm)
		}
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
