// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package keys

import (
	"strings"
	"testing"
)

// Every row shown in the help overlay must carry both a key chord and a
// description — a blank cell would render an empty, confusing line.
func TestOverlaySections_RowsAreComplete(t *testing.T) {
	for _, section := range OverlaySections {
		if section.Title == "" {
			t.Errorf("overlay section with empty title: %+v", section)
		}
		for _, b := range section.Bindings {
			if b.Keys == "" || b.Desc == "" {
				t.Errorf("section %q has an incomplete row: %+v", section.Title, b)
			}
		}
	}
}

// The notifications keybindings are rendered in three places (help bar, overlay,
// panel header); they used to drift ("toggle read" vs "mark read"). Locking the
// single source here guards against that regression.
func TestNotifBindings_ShareOneDescription(t *testing.T) {
	if NotifRead.Desc != "mark read" {
		t.Fatalf("NotifRead.Desc = %q, want %q", NotifRead.Desc, "mark read")
	}
	if !strings.Contains(NotifRead.Bar, NotifRead.Desc) {
		t.Fatalf("NotifRead bar %q should contain its description %q", NotifRead.Bar, NotifRead.Desc)
	}
}
