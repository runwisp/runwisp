// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package logutil

import (
	"encoding/json"
	"os"

	"log/slog"
)

// LogMeta tracks cumulative rotation state so readers can present
// continuous, globally-correct line numbers and byte offsets even after
// multiple tail-mode rotations.
type LogMeta struct {
	RotatedLines int64 `json:"rl"`
	RotatedBytes int64 `json:"rb"`
	FinalLines   int64 `json:"fl,omitempty"`
	Finalized    bool  `json:"fin,omitempty"`
}

func MetaPath(logPath string) string {
	return logPath + ".meta"
}

// ReadLogMeta loads rotation metadata. Returns zero-value if the file is
// missing or corrupt (no rotation has occurred).
func ReadLogMeta(logPath string) LogMeta {
	data, err := os.ReadFile(MetaPath(logPath))
	if err != nil {
		return LogMeta{}
	}
	var meta LogMeta
	if json.Unmarshal(data, &meta) != nil {
		return LogMeta{}
	}
	return meta
}

// WriteLogMeta persists metadata next to the log file.
func WriteLogMeta(logPath string, meta LogMeta) {
	data, err := json.Marshal(meta)
	if err != nil {
		return
	}
	if err := os.WriteFile(MetaPath(logPath), data, 0644); err != nil {
		slog.Warn("Failed to write log metadata", "path", MetaPath(logPath), "err", err)
	}
}
