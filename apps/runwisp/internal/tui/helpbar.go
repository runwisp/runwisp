// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
)

// helpPrio ranks a help-bar hint by how long it survives a narrow terminal.
type helpPrio int

const (
	prioQuit   helpPrio = iota // dropped first: q/^C quit
	prioNav                    // movement, enter/esc, filters, panel switches
	prioAction                 // what the context lets you do: run, stop, retry...
	prioHelp                   // `? help`, never dropped: it leads to every other key
)

// helpSeg is one whole hint; the bar drops segments, never cuts one.
type helpSeg struct {
	text string
	prio helpPrio
}

// helpBar is the bottom bar's hints in display order.
type helpBar []helpSeg

const helpBarSep = "  "

// add appends texts at prio, skipping empty ones.
func (b helpBar) add(prio helpPrio, texts ...string) helpBar {
	for _, t := range texts {
		if t != "" {
			b = append(b, helpSeg{t, prio})
		}
	}
	return b
}

func (b helpBar) String() string {
	texts := make([]string, len(b))
	for i, s := range b {
		texts[i] = s.text
	}
	return strings.Join(texts, helpBarSep)
}

// fit returns the bar within width cells, dropping the lowest-priority
// segment (rightmost first among equals) until it fits. prioHelp segments are
// never dropped, so a bar too narrow even for them still shows them.
func (b helpBar) fit(width int) string {
	keep := slices.Clone(b)
	for lipgloss.Width(keep.String()) > width {
		drop := -1
		for i, s := range keep {
			if s.prio != prioHelp && (drop < 0 || s.prio <= keep[drop].prio) {
				drop = i
			}
		}
		if drop < 0 {
			break
		}
		keep = slices.Delete(keep, drop, drop+1)
	}
	return keep.String()
}
