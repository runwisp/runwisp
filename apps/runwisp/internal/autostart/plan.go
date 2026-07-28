// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package autostart

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/pmezard/go-difflib/difflib"
)

// hashContent returns the short hex SHA-256 used in the unit's
// `# runwisp-config-hash:` marker. Twelve hex chars is plenty for a
// human-eyeballed drift check (the unit is rewritten on every install,
// so collisions are not a security concern).
func hashContent(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:12]
}

// stripGeneratedHashes removes our own hash markers from a unit body
// before computing the *settings* hash. Otherwise the hash would
// depend on itself — and a NoOp install would oscillate. The
// runwisp-masked-cron marker is stripped for the same reason even though
// it isn't self-referential: it records live take-over state, not a
// setting, so it must not be able to flip the settings hash on its own.
func stripGeneratedHashes(body []byte) []byte {
	lines := strings.Split(string(body), "\n")
	kept := make([]string, 0, len(lines))
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "# runwisp-config-hash:") ||
			strings.HasPrefix(t, "# runwisp-binary-sha256:") ||
			strings.HasPrefix(t, "# runwisp-masked-cron:") ||
			strings.HasPrefix(t, "<!-- runwisp-config-hash:") ||
			strings.HasPrefix(t, "<!-- runwisp-binary-sha256:") ||
			strings.HasPrefix(t, "<!-- runwisp-masked-cron:") {
			continue
		}
		kept = append(kept, l)
	}
	return []byte(strings.Join(kept, "\n"))
}

// SettingsHash returns the deterministic hash baked into the unit
// header. It is computed over the *settings*, not the rendered file,
// so adding a comment line does not flip the value.
func SettingsHash(binary, config, dataDir, host string, port int) string {
	repr := fmt.Sprintf("bin=%s\nconfig=%s\ndata=%s\nhost=%s\nport=%d\n",
		binary, config, dataDir, host, port)
	return hashContent([]byte(repr))
}

// parsedUnit is what extractMarkers returns.
type parsedUnit struct {
	managed    bool
	configHash string
	binarySHA  string
	// maskedCron is the cron unit this file's runwisp-masked-cron marker
	// names, empty if the file has none. Only ever set on a file we wrote
	// ourselves — reading it back is how a later plain `service install`
	// (TakeOverCron false) knows to carry the marker forward instead of
	// erasing it, and how uninstall knows it may unmask that unit.
	maskedCron string
}

// managedMarkerBare is the text following the comment-syntax leader.
// Both systemd ("# ManagedMarker") and launchd ("<!-- ManagedMarker -->")
// use the same payload — the marker has to render through whichever
// comment form the file format requires.
const managedMarkerBare = "Managed by runwisp service install — DO NOT EDIT"

// extractMarkers reads the marker comments at the top of a unit file
// and reports whether it is a managed unit and what hashes it carries.
// It tolerates both the systemd "#" comment form and the launchd
// "<!-- … -->" comment form.
// markerFields lists every "# key: value" / "<!-- key: value -->" marker
// extractMarkers looks for, in both the systemd-comment and launchd-XML
// spellings, so adding a new marker never grows extractMarkers itself.
func markerFields(out *parsedUnit) []struct {
	prefix string
	suffix string
	dest   *string
} {
	return []struct {
		prefix string
		suffix string
		dest   *string
	}{
		{"# runwisp-config-hash:", "", &out.configHash},
		{"# runwisp-binary-sha256:", "", &out.binarySHA},
		{"# runwisp-masked-cron:", "", &out.maskedCron},
		{"<!-- runwisp-config-hash:", "-->", &out.configHash},
		{"<!-- runwisp-binary-sha256:", "-->", &out.binarySHA},
		{"<!-- runwisp-masked-cron:", "-->", &out.maskedCron},
	}
}

func extractMarkers(body []byte) parsedUnit {
	out := parsedUnit{}
	fields := markerFields(&out)
	for i, l := range strings.Split(string(body), "\n") {
		if i > 12 { // markers live in the first ~6 lines, allow some slack
			break
		}
		t := strings.TrimSpace(l)
		if t == ManagedMarker || stripCommentMarkers(t) == managedMarkerBare {
			out.managed = true
			continue
		}
		for _, f := range fields {
			if v, ok := strings.CutPrefix(t, f.prefix); ok {
				*f.dest = strings.TrimSpace(strings.TrimSuffix(v, f.suffix))
			}
		}
	}
	return out
}

// stripCommentMarkers removes the leading "# " or wrapping "<!-- … -->"
// from a trimmed line so the marker text can be compared verbatim.
func stripCommentMarkers(s string) string {
	if v, ok := strings.CutPrefix(s, "# "); ok {
		return strings.TrimSpace(v)
	}
	if v, ok := strings.CutPrefix(s, "<!--"); ok {
		v = strings.TrimSuffix(v, "-->")
		return strings.TrimSpace(v)
	}
	return s
}

// ClassifyExisting reads the on-disk unit (if any) and computes the
// PlanKind for a fresh install with the given desired content. The
// returned Plan has Kind, Reason, Diff, and UnitContent populated;
// the caller adds steps and resolved settings.
func ClassifyExisting(fsys FileSystem, unitPath string, desired []byte, force bool) (Plan, error) {
	existing, err := fsys.ReadFile(unitPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Plan{
				Kind:        PlanInstall,
				Reason:      "no unit file at " + unitPath,
				UnitContent: string(desired),
			}, nil
		}
		return Plan{}, fmt.Errorf("read existing unit: %w", err)
	}

	parsed := extractMarkers(existing)
	if !parsed.managed {
		p := Plan{
			Kind:        PlanConflict,
			Reason:      "unit file is not managed by runwisp",
			UnitContent: string(desired),
		}
		if force {
			p.Kind = PlanUpdate
			p.Reason = "unit file is not managed by runwisp — proceeding because --force was passed"
			p.Diff = unifiedDiff(existing, desired, unitPath)
		}
		return p, nil
	}

	desiredHash := hashContent(stripGeneratedHashes(desired))
	existingHash := hashContent(stripGeneratedHashes(existing))
	if desiredHash == existingHash {
		return Plan{
			Kind:        PlanNoop,
			Reason:      "unit content is up to date",
			UnitContent: string(desired),
		}, nil
	}
	return Plan{
		Kind:        PlanUpdate,
		Reason:      "unit content has drifted",
		UnitContent: string(desired),
		Diff:        unifiedDiff(existing, desired, unitPath),
	}, nil
}

// ClassifyUninstall computes the PlanKind for uninstall.
func ClassifyUninstall(fsys FileSystem, unitPath string, force bool) (Plan, error) {
	existing, err := fsys.ReadFile(unitPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Plan{
				Kind:   PlanNoop,
				Reason: "no unit file at " + unitPath,
			}, nil
		}
		return Plan{}, fmt.Errorf("read existing unit: %w", err)
	}
	parsed := extractMarkers(existing)
	if !parsed.managed && !force {
		return Plan{
			Kind:   PlanConflict,
			Reason: "unit file is not managed by runwisp — uninstall would remove a hand-written file; use --force to proceed",
		}, nil
	}
	return Plan{
		Kind:   PlanUninstall,
		Reason: "unit file is present and managed",
	}, nil
}

// unifiedDiff returns a colour-free unified diff for display in the
// confirmation prompt. The caller is responsible for any colouring.
func unifiedDiff(oldB, newB []byte, label string) string {
	d := difflib.UnifiedDiff{
		A:        difflib.SplitLines(string(oldB)),
		B:        difflib.SplitLines(string(newB)),
		FromFile: label + " (on disk)",
		ToFile:   label + " (would write)",
		Context:  3,
	}
	out, _ := difflib.GetUnifiedDiffString(d)
	return out
}
