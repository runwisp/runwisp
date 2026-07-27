// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/tui/uikit"
)

// paramField is one editable row in the param form. Exactly one of the input
// modes is active, decided by the declared parameter kind: flags toggle a
// boolean, strict choices cycle a fixed list, combos cycle a fixed list plus a
// trailing "custom" slot that reveals a free-text input, everything else edits
// free text.
type paramField struct {
	param  model.TaskParam
	input  textinput.Model // free text, and the combo "custom" value
	flagOn bool            // ParamFlag only
	strict bool            // choices set and allow_custom == false
	combo  bool            // choices set and allow_custom == true (choices + custom slot)
	opts   []string        // strict/combo choice options ("" = leave unset)
	selIdx int             // index into opts; for a combo, len(opts) is the custom slot
	// includeOverride sticks the operator's explicit include/omit choice for a
	// free-text value field, made with ctrl+t. Nil means "auto": a blank field
	// is omitted, a filled one is sent. Once set it no longer tracks emptiness,
	// so the operator can force an empty string (include + blank) or drop a
	// filled value (omit). Unused by flags and strict selectors.
	includeOverride *bool
}

// comboSlots is the number of selector positions in a combo field: one per
// option plus the trailing "custom" slot.
func (f *paramField) comboSlots() int { return len(f.opts) + 1 }

// onCustom reports whether a combo selector sits on its trailing "custom" slot,
// where the free-text input is active.
func (f *paramField) onCustom() bool { return f.combo && f.selIdx == len(f.opts) }

// cycleSel steps a combo/strict selector across its options and (for combos) the
// custom slot, wrapping at both ends.
func (f *paramField) cycleSel(delta int) {
	n := f.comboSlots()
	f.selIdx = (f.selIdx + delta + n) % n
}

// comboLabel is the text shown in the selector for a combo's current position:
// the custom slot, an explicit "(unset)" for the optional empty slot, or the
// chosen option verbatim.
func (f *paramField) comboLabel() string {
	if f.onCustom() {
		return "✎ custom…"
	}
	if v := f.opts[f.selIdx]; v != "" {
		return v
	}
	return "(unset)"
}

// focusPart distinguishes the two focus stops a field can offer. Every field has
// a focusMain stop (flag toggle / strict selector / combo selector / plain text
// input); a combo sitting on its custom slot adds a focusCustom stop for the
// revealed free-text input, so ←/→ there move the text cursor instead of cycling
// the selector.
type focusPart int

const (
	focusMain focusPart = iota
	focusCustom
)

// ParamFormDialog collects operator-supplied values for a task's declared
// parameters before a manual trigger. It never *defines* parameters — kinds,
// keys, choices and defaults all come from runwisp.toml; the form only gathers
// values, mirroring the daemon's resolve rules so mistakes surface before the
// request is sent.
type ParamFormDialog struct {
	taskName string
	fields   []paramField
	focus    int
	part     focusPart
	submit   func(map[string]*string) tea.Cmd
	errLine  string
}

// NewParamFormDialog builds a form for the task's parameters. submit is invoked
// with the collected identity→value map when the operator confirms a valid form.
func NewParamFormDialog(taskName string, params []model.TaskParam, submit func(map[string]*string) tea.Cmd) ParamFormDialog {
	fields := make([]paramField, 0, len(params))
	for _, p := range params {
		fields = append(fields, newParamField(p))
	}
	d := ParamFormDialog{taskName: taskName, fields: fields, submit: submit}
	d.syncFocus()
	return d
}

func newParamField(p model.TaskParam) paramField {
	f := paramField{param: p}
	switch {
	case p.Kind == model.ParamFlag:
		f.flagOn = p.Default != nil && *p.Default == "true"
	case len(p.Choices) > 0 && !p.AllowCustom:
		f.strict = true
		// Optional choices get a leading "unset" slot so the operator can
		// decline to supply a value; required ones must pick a member.
		if !p.Required {
			f.opts = append(f.opts, "")
		}
		f.opts = append(f.opts, p.Choices...)
		f.selIdx = defaultIndex(f.opts, p.Default)
	case len(p.Choices) > 0 && p.AllowCustom:
		// A selector over the declared choices plus a trailing "custom" slot: while
		// a choice is selected the value is that choice; selecting custom reveals a
		// free-text input the operator fills in. The TUI counterpart of the web
		// ComboBox — pick or type, no floating popup.
		f.combo = true
		if !p.Required {
			f.opts = append(f.opts, "")
		}
		f.opts = append(f.opts, p.Choices...)
		f.input = newTextInput(nil)
		seedCombo(&f, p.Default)
	default:
		f.input = newTextInput(p.Default)
	}
	return f
}

// seedCombo positions a combo field's selector from its declared default: a
// default naming a listed choice highlights it; any other non-empty default is
// taken as a custom value (selector on the custom slot, input pre-filled); an
// absent/empty default starts on the first slot.
func seedCombo(f *paramField, def *string) {
	if def == nil || *def == "" {
		f.selIdx = 0
		return
	}
	if i := comboSeedIndex(f.opts, def); i >= 0 {
		f.selIdx = i
		return
	}
	f.selIdx = len(f.opts) // custom slot
	f.input.SetValue(*def)
	f.input.CursorEnd()
}

// defaultIndex returns the index of def within opts, or 0 when def is nil or
// absent — the seed for a strict selector's highlighted choice.
func defaultIndex(opts []string, def *string) int {
	if i := comboSeedIndex(opts, def); i >= 0 {
		return i
	}
	return 0
}

// comboSeedIndex returns the index of def within opts, or -1 when def is nil or
// not a listed choice (so callers can fall back to a default slot or the custom
// slot).
func comboSeedIndex(opts []string, def *string) int {
	if def == nil {
		return -1
	}
	for i, o := range opts {
		if o == *def {
			return i
		}
	}
	return -1
}

func newTextInput(def *string) textinput.Model {
	ti := textinput.New()
	ti.CharLimit = 1024
	ti.Width = 40
	// Dress the input in the modal's surface colors. Left at defaults, the
	// focused cursor reverses against the terminal default and renders as a
	// jarring white block on an otherwise empty field; the prompt, text and
	// padding default to no background and stand out against ColorBgLight.
	surface := lipgloss.NewStyle().Background(uikit.ColorBgLight)
	ti.PromptStyle = surface.Foreground(uikit.ColorTextMuted)
	ti.TextStyle = surface.Foreground(uikit.ColorText)
	ti.PlaceholderStyle = surface.Foreground(uikit.ColorTextMuted)
	ti.Cursor.Style = surface.Foreground(uikit.ColorPrimary)
	ti.Cursor.TextStyle = surface.Foreground(uikit.ColorText)
	if def != nil {
		ti.SetValue(*def)
	}
	return ti
}

// supplied collects the operator-supplied map. Every declared parameter gets a
// key so a blank field reads as an explicit omit (nil) rather than an absent key
// the daemon would fill from the default — clearing a field means "don't pass
// it", not "use the default".
func (d *ParamFormDialog) supplied() map[string]*string {
	out := make(map[string]*string, len(d.fields))
	for i := range d.fields {
		out[d.fields[i].param.Key] = d.fields[i].suppliedValue()
	}
	return out
}

// suppliedValue returns the pointer to send for a field: nil omits the parameter
// (not passed at all), a non-nil pointer is the exact value to send including the
// empty string. Flags always send "true"/"false"; an unset selector omits; a
// free-text field auto-omits when blank unless the operator forced inclusion.
func (f *paramField) suppliedValue() *string {
	switch {
	case f.param.Kind == model.ParamFlag:
		v := "false"
		if f.flagOn {
			v = "true"
		}
		return &v
	case f.strict, f.combo && !f.onCustom():
		if v := f.opts[f.selIdx]; v != "" {
			return &v
		}
		return nil
	default:
		v := f.input.Value()
		if f.included() {
			return &v
		}
		return nil
	}
}

// supportsInclude reports whether a field offers the ctrl+t include/omit toggle —
// only free-text value fields (plain text, or a combo sitting on its custom slot),
// where a blank value is ambiguous between "omit" and "empty string". Flags are
// already a single on/off control; strict selectors omit via their "(unset)" slot.
func (f *paramField) supportsInclude() bool {
	if f.param.Kind == model.ParamFlag || f.strict {
		return false
	}
	if f.combo {
		return f.onCustom()
	}
	return true
}

// included reports whether a free-text field's value is sent. The sticky override
// wins; otherwise it auto-tracks emptiness (blank → omit).
func (f *paramField) included() bool {
	if f.includeOverride != nil {
		return *f.includeOverride
	}
	return strings.TrimSpace(f.input.Value()) != ""
}

// toggleInclude flips and sticks the field's include/omit state, so a blank field
// can be forced to send an empty string or a filled one dropped.
func (f *paramField) toggleInclude() {
	next := !f.included()
	f.includeOverride = &next
}

// includeMarker annotates a free-text field's value line with what it will send:
// nothing for a plain value, "(omitted)" when dropped, "(empty)" when an empty
// string is explicitly included.
func (f *paramField) includeMarker() string {
	if !f.supportsInclude() {
		return ""
	}
	if !f.included() {
		return "  (omitted)"
	}
	if f.input.Value() == "" {
		return "  (empty)"
	}
	return ""
}

// Update dispatches input to the form. Returns a command (the submit command
// when the operator confirms a valid form) and whether the dialog should close.
func (d *ParamFormDialog) Update(msg tea.Msg) (tea.Cmd, bool) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil, false
	}
	switch keyMsg.String() {
	case "esc":
		return nil, true
	case "enter":
		return d.trySubmit()
	case "tab", "down":
		d.focusNext()
		return nil, false
	case "shift+tab", "up":
		d.focusPrev()
		return nil, false
	}
	return d.handleFieldKey(keyMsg)
}

func (d *ParamFormDialog) handleFieldKey(keyMsg tea.KeyMsg) (tea.Cmd, bool) {
	if len(d.fields) == 0 {
		return nil, false
	}
	f := &d.fields[d.focus]
	// ctrl+t toggles include/omit for a free-text field, before the textinput
	// sees the key, so it never collides with editing. Lets the operator force an
	// empty string (include + blank) or drop a filled value (omit).
	if keyMsg.String() == "ctrl+t" && f.supportsInclude() {
		f.toggleInclude()
		return nil, false
	}
	// On the custom input stop every key edits text — ←/→ move the cursor like
	// any other text field, the regression this model fixes.
	if d.part == focusCustom {
		var cmd tea.Cmd
		f.input, cmd = f.input.Update(keyMsg)
		return cmd, false
	}
	return f.handleMainKey(keyMsg), false
}

// handleMainKey routes a key on a field's primary stop by kind: flags toggle,
// strict and combo selectors cycle on ←/→, and a plain text field edits. A combo
// selector only cycles here — its custom input lives on a separate stop, so ←/→
// are never overloaded.
func (f *paramField) handleMainKey(keyMsg tea.KeyMsg) tea.Cmd {
	key := keyMsg.String()
	switch {
	case f.param.Kind == model.ParamFlag:
		f.toggleFlag(key)
	case f.strict:
		f.stepStrict(key)
	case f.combo:
		switch {
		case key == "left" || key == "h":
			f.cycleSel(-1)
		case key == "right" || key == "l":
			f.cycleSel(1)
		}
	default:
		var cmd tea.Cmd
		f.input, cmd = f.input.Update(keyMsg)
		return cmd
	}
	return nil
}

// toggleFlag flips a flag field. A flag is binary, so every key an operator
// instinctively reaches for on a checkbox flips it: space/x, and the same
// left/right (vim h/l) that cycle the neighbouring selectors — so the footer's
// arrow hint is honest for flags too.
func (f *paramField) toggleFlag(key string) {
	switch key {
	case " ", "x", "left", "right", "h", "l":
		f.flagOn = !f.flagOn
	}
}

// stepStrict cycles a strict selector on left/right (vim h/l aliases).
func (f *paramField) stepStrict(key string) {
	switch {
	case key == "left" || key == "h":
		f.selIdx = (f.selIdx - 1 + len(f.opts)) % len(f.opts)
	case key == "right" || key == "l":
		f.selIdx = (f.selIdx + 1) % len(f.opts)
	}
}

// trySubmit validates the collected values the same way the daemon will. On
// success it returns the submit command and signals close; on failure it shows
// the error inline and keeps the form open.
func (d *ParamFormDialog) trySubmit() (tea.Cmd, bool) {
	supplied := d.supplied()
	if _, err := model.ResolveParamValues(d.paramList(), supplied); err != nil {
		d.errLine = err.Error()
		return nil, false
	}
	return d.submit(supplied), true
}

func (d *ParamFormDialog) paramList() []model.TaskParam {
	params := make([]model.TaskParam, len(d.fields))
	for i, f := range d.fields {
		params[i] = f.param
	}
	return params
}

// hasCustomRow reports whether field i currently offers a second focus stop: a
// combo sitting on its custom slot, whose revealed input is independently
// focusable.
func (d *ParamFormDialog) hasCustomRow(i int) bool {
	return d.fields[i].combo && d.fields[i].onCustom()
}

// focusNext advances one focus stop: from a combo's selector into its revealed
// custom input, otherwise to the next field's main stop (wrapping).
func (d *ParamFormDialog) focusNext() {
	if len(d.fields) == 0 {
		return
	}
	if d.part == focusMain && d.hasCustomRow(d.focus) {
		d.part = focusCustom
	} else {
		d.focus = (d.focus + 1) % len(d.fields)
		d.part = focusMain
	}
	d.syncFocus()
}

// focusPrev steps back one focus stop: from a custom input back to its selector,
// otherwise to the previous field, landing on its custom input when it has one.
func (d *ParamFormDialog) focusPrev() {
	if len(d.fields) == 0 {
		return
	}
	if d.part == focusCustom {
		d.part = focusMain
	} else {
		d.focus = (d.focus - 1 + len(d.fields)) % len(d.fields)
		if d.hasCustomRow(d.focus) {
			d.part = focusCustom
		} else {
			d.part = focusMain
		}
	}
	d.syncFocus()
}

// syncFocus focuses the text input hosting the active stop and blurs the rest,
// so only one cursor is ever visible. A plain text field hosts its input on the
// main stop; a combo hosts its custom input on the custom stop. Flags and strict
// selectors have no input.
func (d *ParamFormDialog) syncFocus() {
	for i := range d.fields {
		f := &d.fields[i]
		switch {
		case f.param.Kind == model.ParamFlag, f.strict:
			continue
		case f.combo:
			if i == d.focus && d.part == focusCustom {
				f.input.Focus()
			} else {
				f.input.Blur()
			}
		default:
			if i == d.focus && d.part == focusMain {
				f.input.Focus()
			} else {
				f.input.Blur()
			}
		}
	}
}

func (d *ParamFormDialog) View(screenWidth, screenHeight int) string {
	dialogWidth, innerWidth := modalDimensions(screenWidth, 56, 48)

	lines := []string{
		modalEmptyLine(innerWidth),
		modalSurfaceLine("Run "+d.taskName, innerWidth, uikit.ColorTextBright, true),
		modalEmptyLine(innerWidth),
	}
	for i := range d.fields {
		lines = append(lines, d.renderField(i, innerWidth)...)
	}
	if d.errLine != "" {
		lines = append(lines,
			modalLeftLine("⚠ "+d.errLine, innerWidth, uikit.ColorError),
		)
	}
	lines = append(lines,
		modalEmptyLine(innerWidth),
		modalSurfaceLine(d.footerHint(), innerWidth, uikit.ColorTextMuted, false),
		modalEmptyLine(innerWidth),
	)

	box := renderModalBox(screenWidth, screenHeight, dialogWidth, uikit.ColorPrimary, lines)
	return box.view
}

// footerHint builds the dialog's key legend, leading with the action that
// applies to the focused field's active stop so the bar never advertises a key
// that does nothing here (the static "←/→ choose" used to mislead on flags). The
// always-present keys — move, run, cancel — trail every variant.
func (d *ParamFormDialog) footerHint() string {
	const tail = "↑/↓ move · enter run · esc cancel"
	lead := ""
	if len(d.fields) > 0 {
		f := d.fields[d.focus]
		switch {
		case f.param.Kind == model.ParamFlag:
			lead = "space toggle"
		case d.part == focusCustom, !f.strict && !f.combo:
			lead = "type to edit · ctrl+t omit/empty"
		default: // strict or combo selector on its main stop
			lead = "←/→ choose"
		}
	}
	if lead == "" {
		return tail
	}
	return lead + " · " + tail
}

func (d *ParamFormDialog) renderField(i, innerWidth int) []string {
	f := d.fields[i]
	focused := i == d.focus

	label := f.param.Key
	if f.param.Required {
		label += " *"
	}
	labelFg := uikit.ColorText
	prefix := "  "
	if focused {
		labelFg = uikit.ColorTextBright
		prefix = "▶ "
	}

	out := []string{modalLeftLine(prefix+label, innerWidth, labelFg)}

	var value string
	switch {
	case f.param.Kind == model.ParamFlag:
		// The label line already names the flag; the value line only shows its
		// on/off state so the key isn't repeated.
		box := "[ ]"
		state := "off"
		if f.flagOn {
			box = "[x]"
			state = "on"
		}
		value = box + " " + state
	case f.strict:
		cur := f.opts[f.selIdx]
		if cur == "" {
			cur = "(unset)"
		}
		value = "‹ " + cur + " ›"
	case f.combo:
		value = "‹ " + f.comboLabel() + " ›"
	default:
		value = f.input.View() + f.includeMarker()
	}
	if focused && f.param.Kind == model.ParamFlag {
		// A focused flag carries its toggle keys inline, right at the control, so
		// the operator never has to discover them by trial — the muted cue sits
		// beside the [x]/[ ] state and disappears once focus moves on.
		out = append(out, modalLeftLineRich(innerWidth,
			styledSeg("    "+value, uikit.ColorText),
			styledSeg("      space / ←→ toggle", uikit.ColorTextMuted),
		))
	} else {
		out = append(out, modalLeftLine("    "+value, innerWidth, uikit.ColorText))
	}

	if f.onCustom() {
		// The custom slot reveals a fillable text input directly beneath the
		// selector — its own focus stop, so it carries a caret when focused
		// (alongside the input's own blinking cursor).
		rowPrefix := "      "
		if focused && d.part == focusCustom {
			rowPrefix = "    ▸ "
		}
		out = append(out, modalLeftLine(rowPrefix+f.input.View()+f.includeMarker(), innerWidth, uikit.ColorText))
	}
	if f.param.Description != "" {
		out = append(out, modalLeftLine("    "+f.param.Description, innerWidth, uikit.ColorTextMuted))
	}
	return out
}

// modalLeftLine renders a left-aligned full-width line on the modal surface,
// the form counterpart to the centered modalSurfaceLine.
func modalLeftLine(text string, innerWidth int, fg lipgloss.Color) string {
	return lipgloss.NewStyle().
		Background(uikit.ColorBgLight).
		Foreground(fg).
		Width(innerWidth).
		Align(lipgloss.Left).
		Render(text)
}

// modalLeftLineRich renders a left-aligned full-width modal line built from
// independently coloured segments (see styledSeg), padding the remainder to
// innerWidth on the modal surface. The two-tone counterpart to modalLeftLine,
// for rows where a value and a muted hint share one line.
func modalLeftLineRich(innerWidth int, segments ...string) string {
	return lipgloss.NewStyle().
		Background(uikit.ColorBgLight).
		Width(innerWidth).
		Align(lipgloss.Left).
		Render(strings.Join(segments, ""))
}

// styledSeg renders one coloured segment on the modal surface for composing into
// modalLeftLineRich.
func styledSeg(text string, fg lipgloss.Color) string {
	return lipgloss.NewStyle().
		Background(uikit.ColorBgLight).
		Foreground(fg).
		Render(text)
}
