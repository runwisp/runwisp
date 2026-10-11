// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"fmt"
	"image/color"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/tui/uikit"
	"github.com/runwisp/runwisp/apps/runwisp/internal/tui/views/logsearch"
)

// palette lists every colour in uikit/theme.go. A view that paints anything
// else, a stray hex or a bubbles default ANSI grey, fails the test below.
var palette = []color.Color{
	uikit.ColorPrimary, uikit.ColorPrimaryDim, uikit.ColorPrimaryHover,
	uikit.ColorSecondary,
	uikit.ColorBg, uikit.ColorBgLight, uikit.ColorBgRaised,
	uikit.ColorSidebarActive, uikit.ColorSidebarHover,
	uikit.ColorText, uikit.ColorTextMuted, uikit.ColorTextBright, uikit.ColorTextDim,
	uikit.ColorSuccess, uikit.ColorError, uikit.ColorErrorHover,
	uikit.ColorWarning, uikit.ColorWarningHover,
	uikit.ColorRunning, uikit.ColorPending,
	uikit.ColorButtonRaised, uikit.ColorButtonRaisedHover,
}

var sgrRe = regexp.MustCompile(`\x1b\[([0-9;:]*)m`)

// offPalette returns every SGR colour in s that isn't a palette entry, as
// "38;2;r;g;b"-style strings. Basic and 256-colour codes are always off.
func offPalette(s string) []string {
	known := map[string]bool{}
	for _, c := range palette {
		r, g, b, _ := c.RGBA()
		known[fmt.Sprintf("%d;%d;%d", r>>8, g>>8, b>>8)] = true
	}
	var bad []string
	for _, m := range sgrRe.FindAllStringSubmatch(s, -1) {
		ps := strings.FieldsFunc(m[1], func(r rune) bool { return r == ';' || r == ':' })
		for i := 0; i < len(ps); i++ {
			n, _ := strconv.Atoi(ps[i])
			switch {
			case (n == 38 || n == 48 || n == 58) && i+1 < len(ps) && ps[i+1] == "2" && i+4 < len(ps):
				if rgb := strings.Join(ps[i+2:i+5], ";"); !known[rgb] {
					bad = append(bad, fmt.Sprintf("%d;2;%s", n, rgb))
				}
				i += 4
			case (n == 38 || n == 48 || n == 58) && i+1 < len(ps) && ps[i+1] == "5":
				bad = append(bad, strings.Join(ps[i:min(i+3, len(ps))], ";"))
				i += 2
			case n >= 30 && n <= 37, n >= 40 && n <= 47, n >= 90 && n <= 97, n >= 100 && n <= 107:
				bad = append(bad, ps[i])
			}
		}
	}
	return bad
}

func TestOffPalette_FlagsForeignColours(t *testing.T) {
	assert.Empty(t, offPalette("\x1b[1;38;2;47;212;221;48;2;12;23;25mok\x1b[m"))
	assert.Equal(t, []string{"38;2;1;2;3", "38;5;240", "37"},
		offPalette("\x1b[38;2;1;2;3m\x1b[38;5;240m\x1b[37m"))
}

// TestViews_UseOnlyPaletteColours renders the main screen and the overlays
// and checks no colour outside the theme reaches the terminal.
func TestViews_UseOnlyPaletteColours(t *testing.T) {
	const w, h = 140, 45
	ls := logsearch.New(nil, "backup-postgres")
	shutdown := NewConfirmDialog("Stop", "Stop it?", nil)
	shutdown.StartShutdown()
	failed := model.ReasonFailed
	views := map[string]string{
		"main":       benchModel(20).View().Content,
		"log search": ls.View(w, h),
		"confirm":    NewConfirmDialog("Run", "Run it?", nil).View(w, h),
		"danger":     NewConfirmDialog("Stop", "Stop it?", nil).Danger().View(w, h),
		"shutdown":   shutdown.View(w, h),
		"param form": NewParamFormDialog("backup", []model.TaskParam{
			{Kind: model.ParamFlag, Key: "--force", Default: strptr("false")},
			{Key: "target", Default: strptr("prod")},
		}, nil).View(w, h),
		"help":        (&HelpDialog{}).View(w, h),
		"new release": NewNewReleaseDialog("1.0.0", "1.1.0").View(w, h),
		"run detail": NewRunDetailDialog(&model.Run{
			ID: "01J000000000000000000000AB", TaskName: "backup", Status: model.PhaseEnded, EndReason: &failed, ExitCode: 1,
		}, false, 1).View(w, h),
	}
	for name, out := range views {
		assert.Empty(t, offPalette(out), name)
	}
}
