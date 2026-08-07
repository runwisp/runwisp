// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package logutil

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"io"
	"os"
)

// The sidecar container is a single hidden file per run holding everything that
// is not the log body itself: rotation metadata, the line index and
// progress-bar frame history. It replaces the old `.idx`, `.meta` and `.fhist`
// files so a plain `ls` of a log directory shows only the `.log` files.
//
// The container is an append-only stream of typed records:
//
//	[type:1][length:uint32 LE][payload:length]
//
// Append-only matches the writer's incremental model (index entries are appended
// as the log grows) and gives the same crash-safety as the old sidecars: a torn
// trailing record after a kill -9 is silently ignored on read and never affects
// the durable `.log`. Records of different types interleave freely; a reader
// buckets them by type. For metadata the last `m` record wins, so the writer
// "rewrites" meta by appending a fresh record.
const (
	recMeta  byte = 'm' // payload: JSON LogMeta
	recIndex byte = 'i' // payload: 8-byte LE byte offset (one per LogIndexInterval lines)
	recFrame byte = 'f' // payload: JSON frameHistoryEntry
)

// sidecarRecordHeaderSize is the fixed per-record header: 1 type byte + a
// uint32 little-endian payload length.
const sidecarRecordHeaderSize = 5

// maxSidecarRecord caps a single record's payload to bound allocation when the
// container is corrupt. Legitimate index/meta records are tiny; frame-history
// records can be large (many rows at full terminal width) but never approach
// this ceiling. A record claiming more is treated as corruption and stops the
// scan, degrading to "no further sidecar data".
const maxSidecarRecord = 32 * 1024 * 1024

// Sidecar is the decoded contents of a run's consolidated container. Any field
// is empty/zero when the corresponding records are absent (e.g. a short run that
// never crossed the index threshold, or a run with no in-place output).
type Sidecar struct {
	Meta   LogMeta
	Index  []int64              // byte offsets, one per LogIndexInterval lines (current segment)
	Frames map[int64][][]string // anchor line number -> recorded whole-region frames
}

// EncodeSidecarRecord frames one typed record ready to append to the container.
func EncodeSidecarRecord(typ byte, payload []byte) []byte {
	buf := make([]byte, sidecarRecordHeaderSize+len(payload))
	buf[0] = typ
	binary.LittleEndian.PutUint32(buf[1:5], uint32(len(payload)))
	copy(buf[5:], payload)
	return buf
}

// MetaRecord encodes a rotation/finalization metadata record.
func MetaRecord(m LogMeta) []byte {
	payload, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	return EncodeSidecarRecord(recMeta, payload)
}

// IndexRecord encodes a line-index record holding the byte offset of a chunk
// boundary. The Nth index record in the current segment is chunk N.
func IndexRecord(offset int64) []byte {
	var payload [8]byte
	binary.LittleEndian.PutUint64(payload[:], uint64(offset))
	return EncodeSidecarRecord(recIndex, payload[:])
}

// FrameRecord encodes one settled commit group's frame history, keyed to its
// anchor (first committed) line number.
func FrameRecord(anchor int64, frames [][]string) ([]byte, error) {
	payload, err := json.Marshal(frameHistoryEntry{N: anchor, Frames: frames})
	if err != nil {
		return nil, err
	}
	return EncodeSidecarRecord(recFrame, payload), nil
}

// ReadSidecar loads a run's container. A missing file yields a zero-value
// Sidecar (no rotation has occurred / no sidecar data was produced). Scanning
// stops at the first short or oversized record, so a torn trailing write is
// tolerated.
func ReadSidecar(logPath string) Sidecar {
	sc := Sidecar{Frames: map[int64][][]string{}}
	f, err := os.Open(MetaPath(logPath))
	if err != nil {
		return sc
	}
	defer f.Close()

	r := bufio.NewReader(f)
	var hdr [sidecarRecordHeaderSize]byte
	for {
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			break // EOF or torn header
		}
		length := binary.LittleEndian.Uint32(hdr[1:5])
		if length > maxSidecarRecord {
			break // corrupt length; stop
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(r, payload); err != nil {
			break // torn trailing payload
		}
		sc.apply(hdr[0], payload)
	}
	return sc
}

// apply folds one decoded record into the Sidecar. Malformed payloads are
// skipped individually so one bad record never discards the rest.
func (sc *Sidecar) apply(typ byte, payload []byte) {
	switch typ {
	case recMeta:
		var m LogMeta
		if json.Unmarshal(payload, &m) == nil {
			sc.Meta = m // last record wins
		}
	case recIndex:
		if len(payload) == 8 {
			sc.Index = append(sc.Index, int64(binary.LittleEndian.Uint64(payload)))
		}
	case recFrame:
		var e frameHistoryEntry
		if json.Unmarshal(payload, &e) == nil {
			sc.Frames[e.N] = e.Frames // last record per anchor wins
		}
	}
}
