// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package logutil

import "path/filepath"

// LogMeta tracks cumulative rotation state so readers can present
// continuous, globally-correct line numbers and byte offsets even after
// multiple tail-mode rotations.
type LogMeta struct {
	RotatedLines int64 `json:"rl"`
	RotatedBytes int64 `json:"rb"`
	FinalLines   int64 `json:"fl,omitempty"`
	Finalized    bool  `json:"fin,omitempty"`
}

// MetaPath returns the consolidated sidecar container path for a log file. The
// container is hidden (leading dot) so a plain `ls` of the log directory shows
// only the `.log` files. It holds the metadata, line index, timestamp index and
// frame history that used to live in separate `.meta`, `.idx`, `.tidx` and
// `.fhist` sidecars.
func MetaPath(logPath string) string {
	return filepath.Join(filepath.Dir(logPath), "."+filepath.Base(logPath)+".meta")
}

// ReadLogMeta loads rotation metadata. Returns zero-value when no metadata
// record has been written yet (no rotation, run still in flight) or the
// container is absent/corrupt.
func ReadLogMeta(logPath string) LogMeta {
	return ReadSidecar(logPath).Meta
}
