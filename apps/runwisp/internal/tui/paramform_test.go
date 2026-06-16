// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/runwisp/runwisp/internal/model"
)

func strptr(s string) *string { return &s }

// TestParamForm_DefaultsAndFlagToggle verifies flags seed from their default
// and toggle with space, and that submit collects the canonical map.
func TestParamForm_DefaultsAndFlagToggle(t *testing.T) {
	var got map[string]*string
	submit := func(m map[string]*string) tea.Cmd {
		got = m
		return func() tea.Msg { return nil }
	}
	params := []model.TaskParam{
		{Kind: model.ParamFlag, Key: "--force", Default: strptr("false")},
	}
	d := NewParamFormDialog("backup", params, submit)

	// space toggles the focused flag on
	_, closed := d.Update(keyMsgSpecial(tea.KeySpace))
	assert.False(t, closed)

	cmd, closed := d.Update(keyMsgSpecial(tea.KeyEnter))
	require.True(t, closed)
	require.NotNil(t, cmd)
	assert.Equal(t, map[string]*string{"--force": strptr("true")}, got)
}

// TestParamForm_RequiredMissingKeepsOpen verifies a required value with no
// input keeps the form open and surfaces an inline error.
func TestParamForm_RequiredMissingKeepsOpen(t *testing.T) {
	called := false
	submit := func(map[string]*string) tea.Cmd {
		called = true
		return nil
	}
	params := []model.TaskParam{
		{Kind: model.ParamArg, Key: "source", Required: true},
	}
	d := NewParamFormDialog("backup", params, submit)

	cmd, closed := d.Update(keyMsgSpecial(tea.KeyEnter))
	assert.False(t, closed)
	assert.Nil(t, cmd)
	assert.False(t, called)
	assert.NotEmpty(t, d.errLine)
}

// TestParamForm_StrictChoiceCycle verifies left/right cycles a strict choice
// and the selected member is submitted.
func TestParamForm_StrictChoiceCycle(t *testing.T) {
	var got map[string]*string
	submit := func(m map[string]*string) tea.Cmd {
		got = m
		return func() tea.Msg { return nil }
	}
	params := []model.TaskParam{
		{Kind: model.ParamOption, Key: "--region", Required: true, Choices: []string{"us", "eu"}},
	}
	d := NewParamFormDialog("deploy", params, submit)
	// required strict choice with no default starts at first member ("us")
	_, _ = d.Update(keyMsgSpecial(tea.KeyRight)) // -> "eu"

	_, closed := d.Update(keyMsgSpecial(tea.KeyEnter))
	require.True(t, closed)
	assert.Equal(t, map[string]*string{"--region": strptr("eu")}, got)
}

// TestParamForm_TextValueSubmitted verifies free-text values flow through.
func TestParamForm_TextValueSubmitted(t *testing.T) {
	var got map[string]*string
	submit := func(m map[string]*string) tea.Cmd {
		got = m
		return func() tea.Msg { return nil }
	}
	params := []model.TaskParam{
		{Kind: model.ParamEnv, Key: "PROJECT_ID", Required: true},
	}
	d := NewParamFormDialog("sync", params, submit)
	for _, r := range "acme" {
		_, _ = d.Update(keyMsg(string(r)))
	}
	_, closed := d.Update(keyMsgSpecial(tea.KeyEnter))
	require.True(t, closed)
	assert.Equal(t, map[string]*string{"PROJECT_ID": strptr("acme")}, got)
}

// TestParamForm_ComboCycleSubmits verifies that an allow_custom choice param
// selects a listed choice with left/right and submits it.
func TestParamForm_ComboCycleSubmits(t *testing.T) {
	var got map[string]*string
	submit := func(m map[string]*string) tea.Cmd {
		got = m
		return func() tea.Msg { return nil }
	}
	params := []model.TaskParam{
		{Kind: model.ParamOption, Key: "--region", Choices: []string{"us", "eu"}, AllowCustom: true},
	}
	d := NewParamFormDialog("deploy", params, submit)
	require.True(t, d.fields[0].combo, "allow_custom choice must be a combo field")

	// Optional combo seeds on the leading "unset" slot; right -> "us", right -> "eu".
	_, _ = d.Update(keyMsgSpecial(tea.KeyRight))
	_, _ = d.Update(keyMsgSpecial(tea.KeyRight))

	_, closed := d.Update(keyMsgSpecial(tea.KeyEnter))
	require.True(t, closed)
	assert.Equal(t, map[string]*string{"--region": strptr("eu")}, got)
}

// TestParamForm_ComboAcceptsCustomText verifies that selecting the custom slot
// reveals the input, that down moves into it as a separate focus stop, and that
// free text the choices don't cover flows through.
func TestParamForm_ComboAcceptsCustomText(t *testing.T) {
	var got map[string]*string
	submit := func(m map[string]*string) tea.Cmd {
		got = m
		return func() tea.Msg { return nil }
	}
	params := []model.TaskParam{
		{Kind: model.ParamOption, Key: "--region", Choices: []string{"us", "eu"}, AllowCustom: true},
	}
	d := NewParamFormDialog("deploy", params, submit)
	// Slots: [unset, us, eu, custom]; left from unset wraps straight to custom.
	_, _ = d.Update(keyMsgSpecial(tea.KeyLeft))
	require.True(t, d.fields[0].onCustom(), "left from the first slot should land on the custom slot")
	// The custom input is its own focus stop reached with down; only then does
	// typing edit it.
	_, _ = d.Update(keyMsgSpecial(tea.KeyDown))
	require.Equal(t, focusCustom, d.part, "down from the custom selector enters its input")
	for _, r := range "ap-south" {
		_, _ = d.Update(keyMsg(string(r)))
	}
	_, closed := d.Update(keyMsgSpecial(tea.KeyEnter))
	require.True(t, closed)
	assert.Equal(t, map[string]*string{"--region": strptr("ap-south")}, got)
}

// TestParamForm_ComboLeftRightStaysOnField ensures left/right move a combo's
// selector (not field focus), and up/down still move between fields.
func TestParamForm_ComboLeftRightStaysOnField(t *testing.T) {
	submit := func(map[string]*string) tea.Cmd { return nil }
	params := []model.TaskParam{
		{Kind: model.ParamOption, Key: "--region", Choices: []string{"us", "eu"}, AllowCustom: true},
		{Kind: model.ParamEnv, Key: "TOKEN"},
	}
	d := NewParamFormDialog("deploy", params, submit)

	_, _ = d.Update(keyMsgSpecial(tea.KeyRight))
	assert.Equal(t, 0, d.focus, "right on a combo field moves the selector, not focus")
	assert.Equal(t, focusMain, d.part, "right stays on the selector stop")

	// On a non-custom slot the combo has a single stop, so down moves to the
	// next field.
	_, _ = d.Update(keyMsgSpecial(tea.KeyDown))
	assert.Equal(t, 1, d.focus, "down moves to the next field")
	assert.Equal(t, focusMain, d.part)
}

// TestParamForm_ComboCustomInputIsSeparateStop verifies the revealed custom
// input is its own focus stop: down enters it, ←/→ there move the cursor without
// touching the selector, and up returns to the selector still on the custom slot.
func TestParamForm_ComboCustomInputIsSeparateStop(t *testing.T) {
	submit := func(map[string]*string) tea.Cmd { return nil }
	params := []model.TaskParam{
		{Kind: model.ParamOption, Key: "--region", Choices: []string{"us", "eu"}, AllowCustom: true},
	}
	d := NewParamFormDialog("deploy", params, submit)
	// Reach the custom slot, then descend into its input.
	_, _ = d.Update(keyMsgSpecial(tea.KeyLeft))
	require.True(t, d.fields[0].onCustom())
	customSlot := d.fields[0].selIdx

	_, _ = d.Update(keyMsgSpecial(tea.KeyDown))
	require.Equal(t, focusCustom, d.part, "down enters the custom input stop")

	// left on the input stop is a cursor move: selector unchanged, still focused
	// on the input.
	_, _ = d.Update(keyMsgSpecial(tea.KeyLeft))
	assert.Equal(t, focusCustom, d.part, "left on the input stays on the input stop")
	assert.Equal(t, customSlot, d.fields[0].selIdx, "left on the input must not cycle the selector")

	// up returns to the selector, still parked on the custom slot.
	_, _ = d.Update(keyMsgSpecial(tea.KeyUp))
	assert.Equal(t, focusMain, d.part, "up returns to the selector stop")
	assert.True(t, d.fields[0].onCustom(), "selector is still on the custom slot")
}

// TestParamForm_ComboCustomDefaultRevealsInput verifies a default that isn't a
// listed choice seeds the custom slot with the input pre-filled.
func TestParamForm_ComboCustomDefaultRevealsInput(t *testing.T) {
	submit := func(map[string]*string) tea.Cmd { return nil }
	params := []model.TaskParam{
		{Kind: model.ParamOption, Key: "--region", Choices: []string{"us", "eu"}, AllowCustom: true, Default: strptr("custom-1")},
	}
	d := NewParamFormDialog("deploy", params, submit)
	require.True(t, d.fields[0].onCustom(), "a non-choice default seeds the custom slot")
	assert.Equal(t, "custom-1", d.fields[0].input.Value())

	lines := d.renderField(0, 48)
	hasInputRow := false
	for _, l := range lines {
		if strings.Contains(l, "custom-1") {
			hasInputRow = true
		}
	}
	assert.True(t, hasInputRow, "custom slot must render the fillable input row, got: %v", lines)
}

// TestParamForm_FlagRenderNoDuplicateName guards against the value line
// repeating the flag's name (the label line already shows it).
func TestParamForm_FlagRenderNoDuplicateName(t *testing.T) {
	submit := func(map[string]*string) tea.Cmd { return nil }
	params := []model.TaskParam{{Kind: model.ParamFlag, Key: "--force"}}
	d := NewParamFormDialog("t", params, submit)
	lines := d.renderField(0, 48)

	count := 0
	for _, l := range lines {
		if strings.Contains(l, "--force") {
			count++
		}
	}
	assert.Equal(t, 1, count, "flag name should appear once (label only), got lines: %v", lines)
}

// TestParamForm_BlankOptionalOmitted verifies a blank optional free-text field
// is sent as an explicit omit (nil pointer present), not an absent key — so the
// daemon drops it rather than re-injecting a default.
func TestParamForm_BlankOptionalOmitted(t *testing.T) {
	var got map[string]*string
	submit := func(m map[string]*string) tea.Cmd {
		got = m
		return func() tea.Msg { return nil }
	}
	params := []model.TaskParam{
		{Kind: model.ParamEnv, Key: "TOKEN", Default: strptr("seed")},
	}
	d := NewParamFormDialog("sync", params, submit)
	// Clear the pre-filled default so the field is blank.
	d.fields[0].input.SetValue("")

	_, closed := d.Update(keyMsgSpecial(tea.KeyEnter))
	require.True(t, closed)
	require.Contains(t, got, "TOKEN")
	assert.Nil(t, got["TOKEN"], "a cleared field omits the param (nil), never falls back to the default")
}

// TestParamForm_CtrlTForcesEmptyString verifies ctrl+t on a blank free-text
// field force-includes it, sending an explicit empty string rather than omitting.
func TestParamForm_CtrlTForcesEmptyString(t *testing.T) {
	var got map[string]*string
	submit := func(m map[string]*string) tea.Cmd {
		got = m
		return func() tea.Msg { return nil }
	}
	params := []model.TaskParam{{Kind: model.ParamEnv, Key: "TOKEN"}}
	d := NewParamFormDialog("sync", params, submit)

	_, closed := d.Update(keyMsgSpecial(tea.KeyCtrlT))
	require.False(t, closed, "ctrl+t toggles include, it does not submit")

	_, closed = d.Update(keyMsgSpecial(tea.KeyEnter))
	require.True(t, closed)
	require.Contains(t, got, "TOKEN")
	require.NotNil(t, got["TOKEN"], "force-included blank field sends a value, not an omit")
	assert.Equal(t, "", *got["TOKEN"], "the forced value is an explicit empty string")
}

// TestParamForm_CtrlTOmitsFilledValue verifies ctrl+t on a filled free-text field
// force-omits it, dropping the typed value.
func TestParamForm_CtrlTOmitsFilledValue(t *testing.T) {
	var got map[string]*string
	submit := func(m map[string]*string) tea.Cmd {
		got = m
		return func() tea.Msg { return nil }
	}
	params := []model.TaskParam{{Kind: model.ParamEnv, Key: "TOKEN"}}
	d := NewParamFormDialog("sync", params, submit)
	for _, r := range "abc" {
		_, _ = d.Update(keyMsg(string(r)))
	}

	_, _ = d.Update(keyMsgSpecial(tea.KeyCtrlT))
	_, closed := d.Update(keyMsgSpecial(tea.KeyEnter))
	require.True(t, closed)
	require.Contains(t, got, "TOKEN")
	assert.Nil(t, got["TOKEN"], "force-omit drops the typed value")
}

// TestParamForm_CtrlTIgnoredForFlag verifies ctrl+t has no effect on a flag,
// which is already a single on/off control (no include/omit affordance).
func TestParamForm_CtrlTIgnoredForFlag(t *testing.T) {
	var got map[string]*string
	submit := func(m map[string]*string) tea.Cmd {
		got = m
		return func() tea.Msg { return nil }
	}
	params := []model.TaskParam{{Kind: model.ParamFlag, Key: "--force"}}
	d := NewParamFormDialog("t", params, submit)

	_, _ = d.Update(keyMsgSpecial(tea.KeyCtrlT))
	assert.Nil(t, d.fields[0].includeOverride, "ctrl+t must not set an override on a flag")

	_, closed := d.Update(keyMsgSpecial(tea.KeyEnter))
	require.True(t, closed)
	require.NotNil(t, got["--force"])
	assert.Equal(t, "false", *got["--force"], "flag still resolves to its off state")
}

// TestParamForm_EscCancels verifies esc closes the form without submitting.
func TestParamForm_EscCancels(t *testing.T) {
	called := false
	submit := func(map[string]*string) tea.Cmd { called = true; return nil }
	params := []model.TaskParam{{Kind: model.ParamFlag, Key: "--v"}}
	d := NewParamFormDialog("t", params, submit)

	cmd, closed := d.Update(keyMsgSpecial(tea.KeyEsc))
	assert.True(t, closed)
	assert.Nil(t, cmd)
	assert.False(t, called)
}
