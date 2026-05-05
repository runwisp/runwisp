// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package render

import (
	"fmt"
	"math"
	"time"
)

// RhythmInput is what both Phrase and Sparkline consume. Mirrors the SSE
// payload shape so the same data structure flows between Go (TUI) and TS
// (Web UI) without translation.
type RhythmInput struct {
	Count          int
	CreatedAt      time.Time
	LastOccurredAt time.Time
	Occurrences    []time.Time // newest first
	Now            time.Time
}

// Phrase returns the human-readable cadence string. Rules are listed in
// priority order; the first matching rule wins. Mirrored byte-for-byte by
// apps/ui/src/lib/utils/notification-rhythm.ts.
func Phrase(in RhythmInput) string {
	if in.Count <= 1 {
		return Relative(in.LastOccurredAt, in.Now)
	}

	withinHour := allWithin(in.Occurrences, in.Now, time.Hour)
	if withinHour {
		return fmt.Sprintf("%d× in the last hour, latest %s", in.Count, Relative(in.LastOccurredAt, in.Now))
	}

	withinDay := allWithin(in.Occurrences, in.Now, 24*time.Hour)
	if withinDay {
		return fmt.Sprintf("%d× today, latest %s", in.Count, Relative(in.LastOccurredAt, in.Now))
	}

	span := in.Now.Sub(in.CreatedAt)
	if span >= 7*24*time.Hour {
		return fmt.Sprintf("%d× since %s, latest %s", in.Count, Relative(in.CreatedAt, in.Now), Relative(in.LastOccurredAt, in.Now))
	}

	return fmt.Sprintf("%d× over %s, latest %s", in.Count, formatSpan(span), Relative(in.LastOccurredAt, in.Now))
}

// Sparkline returns an 8-cell unicode block sparkline (or shorter when there
// are fewer occurrences). Buckets the occurrences by age relative to `now`,
// over the supplied window. Heavy on the right = recent burst.
func Sparkline(occurrences []time.Time, now time.Time, window time.Duration, cells int) string {
	if cells <= 0 {
		cells = 8
	}
	if len(occurrences) == 0 || window <= 0 {
		return ""
	}
	buckets := make([]int, cells)
	for _, t := range occurrences {
		age := now.Sub(t)
		if age < 0 {
			age = 0
		}
		if age >= window {
			continue
		}
		// Older occurrences land in lower-index buckets (left); newer on the right.
		idx := cells - 1 - int(math.Floor(float64(age)/float64(window)*float64(cells)))
		if idx < 0 {
			idx = 0
		}
		if idx >= cells {
			idx = cells - 1
		}
		buckets[idx]++
	}
	max := 0
	for _, b := range buckets {
		if b > max {
			max = b
		}
	}
	const blocks = "▁▂▃▄▅▆▇█"
	if max == 0 {
		return ""
	}
	out := make([]rune, cells)
	runes := []rune(blocks)
	for i, b := range buckets {
		if b == 0 {
			out[i] = runes[0]
			continue
		}
		idx := int(math.Round(float64(b) / float64(max) * float64(len(runes)-1)))
		if idx < 0 {
			idx = 0
		}
		if idx >= len(runes) {
			idx = len(runes) - 1
		}
		out[i] = runes[idx]
	}
	return string(out)
}

func allWithin(occ []time.Time, now time.Time, window time.Duration) bool {
	if len(occ) == 0 {
		return false
	}
	cutoff := now.Add(-window)
	for _, t := range occ {
		if t.Before(cutoff) {
			return false
		}
	}
	return true
}

// Relative returns a short human string ("just now", "5m ago", "yesterday",
// "3d ago"). Identical wording to the TS counterpart.
func Relative(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < 30*time.Second:
		return "just now"
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 48*time.Hour:
		return "yesterday"
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		months := int(d.Hours() / 24 / 30)
		if months <= 1 {
			return "1mo ago"
		}
		return fmt.Sprintf("%dmo ago", months)
	default:
		years := int(d.Hours() / 24 / 365)
		if years <= 1 {
			return "1y ago"
		}
		return fmt.Sprintf("%dy ago", years)
	}
}

func formatSpan(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d < 30*24*time.Hour:
		weeks := int(d.Hours() / 24 / 7)
		if weeks <= 1 {
			return "1 week"
		}
		return fmt.Sprintf("%d weeks", weeks)
	default:
		months := int(d.Hours() / 24 / 30)
		if months <= 1 {
			return "1 month"
		}
		return fmt.Sprintf("%d months", months)
	}
}
