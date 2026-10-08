// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package render_test

import (
	"strings"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/notify"
	"github.com/runwisp/runwisp/internal/notify/render"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- LoadDefaultTemplate ----

func TestLoadDefaultTemplate_UnknownNameReturnsError(t *testing.T) {
	_, err := render.LoadDefaultTemplate("unknown-provider")
	assert.Error(t, err)
}

func TestLoadDefaultTemplate_AllKnownDefaultsLoad(t *testing.T) {
	for _, name := range []string{"slack", "telegram", "inapp"} {
		body, err := render.LoadDefaultTemplate(name)
		assert.NoError(t, err, "provider %q should have an embedded default", name)
		assert.NotEmpty(t, body, "embedded template for %q should not be empty", name)
	}
	// Embedded slack template is JSON — it must contain "blocks".
	body, err := render.LoadDefaultTemplate("slack")
	require.NoError(t, err)
	assert.Contains(t, body, "blocks")
}

// ---- NewTemplateRenderer / Render ----

func TestNewTemplateRenderer_ParseError(t *testing.T) {
	_, err := render.NewTemplateRenderer("bad", "{{ .Unclosed", nil, render.TemplateContext{})
	assert.Error(t, err)
}

func TestNewTemplateRenderer_ValidTemplate(t *testing.T) {
	r, err := render.NewTemplateRenderer("hello", "hello {{ .TaskName }}", nil, render.TemplateContext{})
	require.NoError(t, err)
	assert.NotNil(t, r)
}

func TestTemplateRenderer_Render_BasicSubstitution(t *testing.T) {
	r, err := render.NewTemplateRenderer("t", "task={{ .TaskName }}", nil, render.TemplateContext{})
	require.NoError(t, err)

	ev := &notify.Event{TaskName: "backup"}
	msg, err := r.Render(ev)
	require.NoError(t, err)
	assert.Equal(t, "task=backup", string(msg.Body))
}

func TestTemplateRenderer_Render_TitleFn(t *testing.T) {
	titleFn := func(ev *notify.Event) string { return "title:" + ev.TaskName }
	r, err := render.NewTemplateRenderer("t", "body", titleFn, render.TemplateContext{})
	require.NoError(t, err)

	ev := &notify.Event{TaskName: "job"}
	msg, err := r.Render(ev)
	require.NoError(t, err)
	assert.Equal(t, "title:job", msg.Title)
}

func TestTemplateRenderer_Render_TitleFnNil(t *testing.T) {
	r, err := render.NewTemplateRenderer("t", "body", nil, render.TemplateContext{})
	require.NoError(t, err)

	ev := &notify.Event{TaskName: "job"}
	msg, err := r.Render(ev)
	require.NoError(t, err)
	assert.Equal(t, "", msg.Title)
}

// ---- funcMap functions via template execution ----

func renderWith(t *testing.T, tmpl string, ev *notify.Event) string {
	t.Helper()
	r, err := render.NewTemplateRenderer("test", tmpl, nil, render.TemplateContext{})
	require.NoError(t, err)
	msg, err := r.Render(ev)
	require.NoError(t, err)
	return string(msg.Body)
}

func TestFuncMap_Upper(t *testing.T) {
	ev := &notify.Event{TaskName: "hello"}
	out := renderWith(t, `{{ upper .TaskName }}`, ev)
	assert.Equal(t, "HELLO", out)
}

func TestFuncMap_Lower(t *testing.T) {
	ev := &notify.Event{TaskName: "WORLD"}
	out := renderWith(t, `{{ lower .TaskName }}`, ev)
	assert.Equal(t, "world", out)
}

func TestFuncMap_Trim(t *testing.T) {
	ev := &notify.Event{Reason: "  spaced  "}
	out := renderWith(t, `{{ trim .Reason }}`, ev)
	assert.Equal(t, "spaced", out)
}

func TestFuncMap_HtmlEsc(t *testing.T) {
	ev := &notify.Event{Reason: "<b>bold</b>"}
	out := renderWith(t, `{{ htmlEsc .Reason }}`, ev)
	assert.Equal(t, "&lt;b&gt;bold&lt;/b&gt;", out)
}

func TestFuncMap_JsonStr_PlainString(t *testing.T) {
	ev := &notify.Event{Reason: "all clear"}
	out := renderWith(t, `{{ jsonStr .Reason }}`, ev)
	assert.Equal(t, "all clear", out)
}

func TestFuncMap_JsonStr_EscapesQuote(t *testing.T) {
	ev := &notify.Event{Reason: `say "hello"`}
	out := renderWith(t, `{{ jsonStr .Reason }}`, ev)
	assert.Equal(t, `say \"hello\"`, out)
}

func TestFuncMap_JsonStr_EscapesNewline(t *testing.T) {
	ev := &notify.Event{Reason: "line1\nline2"}
	out := renderWith(t, `{{ jsonStr .Reason }}`, ev)
	assert.Equal(t, `line1\nline2`, out)
}

func TestFuncMap_JsonStr_EscapesBackslash(t *testing.T) {
	ev := &notify.Event{Reason: `back\slash`}
	out := renderWith(t, `{{ jsonStr .Reason }}`, ev)
	assert.Equal(t, `back\\slash`, out)
}

func TestFuncMap_TimeRFC(t *testing.T) {
	fixed := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	ev := &notify.Event{Timestamp: fixed}
	out := renderWith(t, `{{ timeRFC .Timestamp }}`, ev)
	assert.Equal(t, "2024-01-15T10:30:00Z", out)
}

func TestFuncMap_TgEscape_Ampersand(t *testing.T) {
	ev := &notify.Event{Reason: "cats & dogs"}
	out := renderWith(t, `{{ tgEscape .Reason }}`, ev)
	assert.Equal(t, "cats &amp; dogs", out)
}

func TestFuncMap_TgEscape_LtGt(t *testing.T) {
	ev := &notify.Event{Reason: "<b>bold</b>"}
	out := renderWith(t, `{{ tgEscape .Reason }}`, ev)
	assert.Equal(t, "&lt;b&gt;bold&lt;/b&gt;", out)
}

func TestFuncMap_Emoji_Error(t *testing.T) {
	ev := &notify.Event{Severity: notify.SevError}
	out := renderWith(t, `{{ emoji .Severity }}`, ev)
	assert.Equal(t, ":rotating_light:", out)
}

func TestFuncMap_Emoji_Warn(t *testing.T) {
	ev := &notify.Event{Severity: notify.SevWarn}
	out := renderWith(t, `{{ emoji .Severity }}`, ev)
	assert.Equal(t, ":warning:", out)
}

func TestFuncMap_Emoji_Info(t *testing.T) {
	ev := &notify.Event{Severity: notify.SevInfo}
	out := renderWith(t, `{{ emoji .Severity }}`, ev)
	assert.Equal(t, ":information_source:", out)
}

func TestFuncMap_Emoji_Default(t *testing.T) {
	ev := &notify.Event{Severity: notify.Severity("unknown")}
	out := renderWith(t, `{{ emoji .Severity }}`, ev)
	assert.Equal(t, ":information_source:", out)
}

// ---- jsonEscape control characters ----

func TestFuncMap_JsonStr_EscapesTab(t *testing.T) {
	ev := &notify.Event{Reason: "col1\tcol2"}
	out := renderWith(t, `{{ jsonStr .Reason }}`, ev)
	assert.Equal(t, `col1\tcol2`, out)
}

func TestFuncMap_JsonStr_EscapesCarriageReturn(t *testing.T) {
	ev := &notify.Event{Reason: "line\r\n"}
	out := renderWith(t, `{{ jsonStr .Reason }}`, ev)
	assert.Equal(t, `line\r\n`, out)
}

func TestFuncMap_JsonStr_EscapesControlByte(t *testing.T) {
	// ASCII 0x01 is a control char below 0x20 and must be unicode-escaped.
	r, err := render.NewTemplateRenderer("test", "{{ jsonStr .Reason }}", nil, render.TemplateContext{})
	require.NoError(t, err)
	ev := &notify.Event{Reason: string([]byte{0x01})}
	msg, err := r.Render(ev)
	require.NoError(t, err)
	assert.Equal(t, "\\u0001", string(msg.Body))
}

// ---- DefaultTitle ----

func TestDefaultTitle_NilEvent(t *testing.T) {
	title := render.DefaultTitle(nil)
	assert.Equal(t, "", title)
}

func TestDefaultTitle_RunSucceeded(t *testing.T) {
	ev := &notify.Event{Kind: notify.KindRunSucceeded, TaskName: "backup"}
	title := render.DefaultTitle(ev)
	assert.True(t, strings.Contains(title, "backup"), "title should mention task name")
}
