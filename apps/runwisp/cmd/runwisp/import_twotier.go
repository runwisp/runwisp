// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/importer"
	"github.com/runwisp/runwisp/internal/model"
)

// rootWriteMode records what `import --write` did to the root config so the
// summary can report it accurately.
type rootWriteMode int

const (
	rootCreated         rootWriteMode = iota // no config existed; wrote a fresh two-tier root
	rootWired                                // an existing config gained the runwisp.d include
	rootAlreadyIncludes                      // an existing config already covered runwisp.d
)

// emitTwoTier writes an import in the two-tier layout: the per-task TOML lands in
// the machine-owned runwisp.d/imported.toml, and the root config is created (or has
// its [daemon].include wired) so the daemon picks the staging file up on load.
//
// The staging file is machine-owned — `import` manages it — so it is overwritten
// without prompting; the operator's root config is only ever created fresh or
// surgically wired, never clobbered. The whole write is atomic: on a genuine
// conflict it rolls both files back so the daemon's config is never left broken.
// contentErr is the pre-known validation error of the generated content itself
// (an unparseable cron that became a # TODO): when set, the files are kept so the
// operator can fix them in place, matching the single-file --write behavior.
func emitTwoTier(stderr io.Writer, rootPath, stagingContent string, res *importer.Result, sourceLabel string, contentErr error, opts importOpts) error {
	rootDir := filepath.Dir(rootPath)
	stagingPath := config.StagingFilePath(rootDir)

	newRoot, mode, err := planRootWrite(rootPath, rootDir)
	if err != nil {
		return err
	}

	rootBackup := backupFile(rootPath)
	stagingBackup := backupFile(stagingPath)

	if err := os.MkdirAll(filepath.Dir(stagingPath), 0o755); err != nil {
		return &userFacingError{title: fmt.Sprintf("can't create %s", filepath.Dir(stagingPath)), details: err.Error()}
	}
	if err := writeFileAtomic(stagingPath, []byte(stagingContent)); err != nil {
		stagingBackup.restore()
		return &userFacingError{title: fmt.Sprintf("can't write %s", stagingPath), details: err.Error()}
	}
	if newRoot != nil {
		if err := writeFileAtomic(rootPath, newRoot); err != nil {
			rootBackup.restore()
			stagingBackup.restore()
			return &userFacingError{title: fmt.Sprintf("can't write %s", rootPath), details: err.Error()}
		}
	}

	// Validate the merged config. A load failure when the generated content was
	// itself valid means the import collided with the existing config (e.g. a
	// duplicate task name across files) — roll everything back rather than leave
	// the daemon unable to load. When contentErr was already set, the failure is
	// the expected # TODO, so keep the files for the operator to fix.
	if _, loadErr := config.Load(rootPath); loadErr != nil && contentErr == nil {
		rootBackup.restore()
		stagingBackup.restore()
		return &userFacingError{
			title:   "import conflicts with your existing config — nothing was written",
			details: loadErr.Error(),
		}
	}

	printTwoTierSummary(stderr, res, sourceLabel, stagingPath, rootPath, mode, contentErr, opts)
	return nil
}

// printTwoTierSummary reports a two-tier `--write`: the shared item/notes blocks,
// then where the staging file landed, what happened to the root config, and the
// nudge toward `runwisp promote`.
func printTwoTierSummary(w io.Writer, res *importer.Result, sourceLabel, stagingPath, rootPath string, mode rootWriteMode, contentErr error, opts importOpts) {
	if opts.quiet {
		return
	}
	st := newImportStyles()

	tasks, services := res.Counts()
	fmt.Fprintf(w, "\nImported %s → %s\n", sourceLabel, pluralizeCounts(tasks, services))
	printImportItems(w, res, st)
	printImportNotes(w, res, st)

	fmt.Fprintln(w)
	fmt.Fprintf(w, "Staged %s in %s\n", pluralizeCounts(tasks, services), stagingPath)
	glob := config.StagingIncludeGlob
	switch mode {
	case rootCreated:
		fmt.Fprintf(w, "Created %s and wired it to load %s.\n", rootPath, glob)
	case rootWired:
		fmt.Fprintf(w, "Wired %s to load %s.\n", rootPath, glob)
	case rootAlreadyIncludes:
		fmt.Fprintf(w, "%s already loads %s.\n", rootPath, glob)
	}

	if contentErr != nil {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "%s some tasks need a fix before the config validates:\n  %s\n",
			st.attn.Render("!"), contentErr.Error())
		fmt.Fprintf(w, "Resolve the # TODO items in %s, then run `runwisp validate`.\n", stagingPath)
		return
	}

	fmt.Fprintln(w)
	fmt.Fprintf(w, "Validated — the daemon loads these on next start or `runwisp reload`.\n")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "They show as %s: imported, not yet native. Graduate one into\n", st.dim.Render("staged"))
	fmt.Fprintf(w, "%s when you want to own it:\n", filepath.Base(rootPath))
	fmt.Fprintln(w, "  runwisp promote <name>")
}

// planRootWrite decides what bytes the root config should end up with. It never
// touches disk beyond reading the existing root.
func planRootWrite(rootPath, rootDir string) ([]byte, rootWriteMode, error) {
	orig, err := os.ReadFile(rootPath)
	if errors.Is(err, os.ErrNotExist) {
		return []byte(config.TwoTierRootConfig()), rootCreated, nil
	}
	if err != nil {
		return nil, 0, &userFacingError{title: fmt.Sprintf("can't read %s", rootPath), details: err.Error()}
	}

	wired, changed, werr := config.EnsureStagingInclude(orig, rootDir)
	if errors.Is(werr, config.ErrIncludeNeedsManualWiring) {
		return nil, 0, &userFacingError{
			title: fmt.Sprintf("%s already sets a custom [daemon].include", filepath.Base(rootPath)),
			details: fmt.Sprintf(
				"Add %q to that list, then re-run `runwisp import cron --write`. Nothing was written.",
				config.StagingIncludeGlob),
		}
	}
	if werr != nil {
		return nil, 0, &userFacingError{title: fmt.Sprintf("can't update %s", rootPath), details: werr.Error()}
	}
	if !changed {
		return nil, rootAlreadyIncludes, nil
	}
	return wired, rootWired, nil
}

// existingEntryCommands loads the merged config at rootPath and returns the
// task/service names defined in files OTHER than the machine-owned staging file
// (which a re-import overwrites), mapped to each task's run command. Services
// and compose entries map to "" — they have no comparable one-shot command, so
// a name clash with them forces a rename rather than a skip. A missing or
// invalid config yields an empty map (nothing reserved), which is correct for a
// first import.
func existingEntryCommands(rootPath string) map[string]string {
	cfg, err := config.Load(rootPath)
	if err != nil {
		return nil
	}
	out := make(map[string]string, len(cfg.Tasks))
	for i := range cfg.Tasks {
		t := &cfg.Tasks[i]
		if t.Staged {
			continue // the staging file is about to be rewritten wholesale
		}
		if t.Kind == model.KindService {
			out[t.Name] = ""
			continue
		}
		out[t.Name] = t.Run
	}
	return out
}

// fileBackup snapshots a file's prior content so a failed multi-file write can be
// rolled back to exactly its previous state.
type fileBackup struct {
	path    string
	existed bool
	content []byte
}

func backupFile(path string) fileBackup {
	b := fileBackup{path: path}
	if data, err := os.ReadFile(path); err == nil {
		b.existed = true
		b.content = data
	}
	return b
}

// restore returns the file to its snapshotted state: rewriting the prior content,
// or removing the file if it didn't exist before. Best-effort — rollback runs on
// an already-failing path.
func (b fileBackup) restore() {
	if b.existed {
		_ = os.WriteFile(b.path, b.content, 0o644)
		return
	}
	_ = os.Remove(b.path)
}

// writeFileAtomic writes data to path via a temp file in the same directory and
// an atomic rename, so a crash mid-write never leaves a half-written config.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	_, writeErr := tmp.Write(data)
	closeErr := tmp.Close()
	if writeErr != nil {
		_ = os.Remove(tmpName)
		return writeErr
	}
	if closeErr != nil {
		_ = os.Remove(tmpName)
		return closeErr
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}
