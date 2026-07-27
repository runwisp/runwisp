// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package rhythm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type rhythmVector struct {
	Name              string    `json:"name"`
	Count             int       `json:"count"`
	CreatedAt         time.Time `json:"created_at"`
	LastOccurredAt    time.Time `json:"last_occurred_at"`
	Occurrences       []int64   `json:"occurrences_unix_ms"` // newest first
	NowUnixMillis     int64     `json:"now_unix_ms"`
	WindowMillis      int64     `json:"window_ms"`
	Cells             int       `json:"cells"`
	ExpectedPhrase    string    `json:"expected_phrase"`
	ExpectedSparkline string    `json:"expected_sparkline"`
}

func vectorInputs() []rhythmVector {
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	mk := func(offsets ...time.Duration) []int64 {
		out := make([]int64, len(offsets))
		for i, d := range offsets {
			out[i] = now.Add(-d).UnixMilli()
		}
		return out
	}
	return []rhythmVector{
		{
			Name:              "single_just_now",
			Count:             1,
			CreatedAt:         now.Add(-10 * time.Second),
			LastOccurredAt:    now.Add(-10 * time.Second),
			Occurrences:       mk(10 * time.Second),
			NowUnixMillis:     now.UnixMilli(),
			WindowMillis:      int64((time.Hour).Milliseconds()),
			Cells:             8,
			ExpectedPhrase:    "just now",
			ExpectedSparkline: "▁▁▁▁▁▁▁█",
		},
		{
			Name:              "single_5m_ago",
			Count:             1,
			CreatedAt:         now.Add(-5 * time.Minute),
			LastOccurredAt:    now.Add(-5 * time.Minute),
			Occurrences:       mk(5 * time.Minute),
			NowUnixMillis:     now.UnixMilli(),
			WindowMillis:      int64((time.Hour).Milliseconds()),
			Cells:             8,
			ExpectedPhrase:    "5m ago",
			ExpectedSparkline: "▁▁▁▁▁▁▁█",
		},
		{
			Name:           "burst_in_last_hour",
			Count:          12,
			CreatedAt:      now.Add(-50 * time.Minute),
			LastOccurredAt: now.Add(-30 * time.Second),
			Occurrences:    mk(30*time.Second, 5*time.Minute, 10*time.Minute, 15*time.Minute, 20*time.Minute, 25*time.Minute, 30*time.Minute, 35*time.Minute, 40*time.Minute, 45*time.Minute),
			NowUnixMillis:  now.UnixMilli(),
			WindowMillis:   int64((time.Hour).Milliseconds()),
			Cells:          8,
			ExpectedPhrase: "12× in the last hour, latest 30s ago",
		},
		{
			Name:           "twice_since_yesterday",
			Count:          2,
			CreatedAt:      now.Add(-26 * time.Hour),
			LastOccurredAt: now.Add(-1 * time.Minute),
			Occurrences:    mk(1*time.Minute, 26*time.Hour),
			NowUnixMillis:  now.UnixMilli(),
			WindowMillis:   int64((48 * time.Hour).Milliseconds()),
			Cells:          8,
			ExpectedPhrase: "2× over 1d, latest 1m ago",
		},
		{
			Name:           "weekly_for_a_month",
			Count:          4,
			CreatedAt:      now.Add(-28 * 24 * time.Hour),
			LastOccurredAt: now.Add(-1 * time.Minute),
			Occurrences:    mk(1*time.Minute, 7*24*time.Hour, 14*24*time.Hour, 21*24*time.Hour),
			NowUnixMillis:  now.UnixMilli(),
			WindowMillis:   int64((30 * 24 * time.Hour).Milliseconds()),
			Cells:          8,
			ExpectedPhrase: "4× since 28d ago, latest 1m ago",
		},
	}
}

func runVector(v rhythmVector) (string, string) {
	occ := make([]time.Time, len(v.Occurrences))
	for i, ms := range v.Occurrences {
		occ[i] = time.UnixMilli(ms).UTC()
	}
	now := time.UnixMilli(v.NowUnixMillis).UTC()
	in := RhythmInput{
		Count:          v.Count,
		CreatedAt:      v.CreatedAt,
		LastOccurredAt: v.LastOccurredAt,
		Occurrences:    occ,
		Now:            now,
	}
	phrase := Phrase(in)
	spark := Sparkline(occ, now, time.Duration(v.WindowMillis)*time.Millisecond, v.Cells)
	return phrase, spark
}

func TestRelative(t *testing.T) {
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		d    time.Duration
		want string
	}{
		{5 * time.Second, "just now"},
		{45 * time.Second, "45s ago"},
		{90 * time.Second, "1m ago"},
		{30 * time.Minute, "30m ago"},
		{2 * time.Hour, "2h ago"},
		{25 * time.Hour, "yesterday"},
		{5 * 24 * time.Hour, "5d ago"},
		{45 * 24 * time.Hour, "1mo ago"},
		{2 * 30 * 24 * time.Hour, "2mo ago"},
		{400 * 24 * time.Hour, "1y ago"},
		{3 * 365 * 24 * time.Hour, "3y ago"},
	}
	for _, tt := range tests {
		got := Relative(now.Add(-tt.d), now)
		assert.Equal(t, tt.want, got, "Relative with d=%v", tt.d)
	}
}

func TestFormatSpan(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Minute, "30m"},
		{90 * time.Minute, "1h"},
		{3 * 24 * time.Hour, "3d"},
		{10 * 24 * time.Hour, "1 week"},
		{14 * 24 * time.Hour, "2 weeks"},
		{50 * 24 * time.Hour, "1 month"},
		{90 * 24 * time.Hour, "3 months"},
	}
	for _, tt := range tests {
		got := formatSpan(tt.d)
		assert.Equal(t, tt.want, got, "formatSpan(%v)", tt.d)
	}
}

func TestSparkline_Empty(t *testing.T) {
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	assert.Equal(t, "", Sparkline(nil, now, time.Hour, 8))
	assert.Equal(t, "", Sparkline([]time.Time{}, now, time.Hour, 8))
	assert.Equal(t, "", Sparkline([]time.Time{now.Add(-2 * time.Hour)}, now, time.Hour, 8)) // outside window
}

func TestSparkline_DefaultCells(t *testing.T) {
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	occ := []time.Time{now.Add(-5 * time.Minute)}
	out := Sparkline(occ, now, time.Hour, 0) // 0 → defaults to 8
	assert.NotEmpty(t, out)
}

func TestAllWithin_EmptySlice(t *testing.T) {
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	assert.False(t, allWithin(nil, now, time.Hour))
}

func TestRhythm_TableVectors(t *testing.T) {
	for _, v := range vectorInputs() {
		v := v
		t.Run(v.Name, func(t *testing.T) {
			phrase, spark := runVector(v)
			assert.Equal(t, v.ExpectedPhrase, phrase, "phrase mismatch for %s", v.Name)
			if v.ExpectedSparkline != "" {
				assert.Equal(t, v.ExpectedSparkline, spark, "sparkline mismatch for %s", v.Name)
			}
		})
	}
}

// TestRhythm_ExportVectors writes the canonical test vectors to disk so the
// Vitest counterpart in apps/ui/src/lib/utils/notification-rhythm.test.ts
// can assert byte-identical output. Runs only when RHYTHM_EXPORT=1 is set;
// otherwise it's a no-op (CI does not need to write to the source tree).
func TestRhythm_ExportVectors(t *testing.T) {
	if os.Getenv("RHYTHM_EXPORT") != "1" {
		t.Skip("set RHYTHM_EXPORT=1 to regenerate __rhythm_vectors.json")
	}
	vectors := vectorInputs()
	// Fill in expected outputs by running the Go side; the TS test asserts
	// against these.
	for i := range vectors {
		phrase, spark := runVector(vectors[i])
		vectors[i].ExpectedPhrase = phrase
		vectors[i].ExpectedSparkline = spark
	}
	out, err := json.MarshalIndent(vectors, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	dst := filepath.Join("..", "..", "..", "..", "..", "..", "apps", "ui", "src", "lib", "utils", "__rhythm_vectors.json")
	if err := os.WriteFile(dst, append(out, '\n'), 0o644); err != nil {
		t.Fatalf("write %s: %v", dst, err)
	}
}
