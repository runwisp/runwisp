// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package render_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/notify"
	"github.com/runwisp/runwisp/internal/notify/render"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- LoadTemplate ----

func TestLoadTemplate_FallsBackToEmbeddedDefault(t *testing.T) {
	body, err := render.LoadTemplate("slack", "")
	require.NoError(t, err)
	assert.NotEmpty(t, body)
	// Embedded slack template is JSON — it must contain "blocks".
	assert.Contains(t, body, "blocks")
}

func TestLoadTemplate_TrimSpacesUserPath(t *testing.T) {
	body, err := render.LoadTemplate("inapp", "   ")
	require.NoError(t, err)
	assert.NotEmpty(t, body)
}

func TestLoadTemplate_UserPathOverridesDefault(t *testing.T) {
	dir := t.TempDir()
	custom := filepath.Join(dir, "custom.tmpl.txt")
	require.NoError(t, os.WriteFile(custom, []byte("custom body"), 0644))

	body, err := render.LoadTemplate("slack", custom)
	require.NoError(t, err)
	assert.Equal(t, "custom body", body)
}

func TestLoadTemplate_UserPathMissingReturnsError(t *testing.T) {
	_, err := render.LoadTemplate("slack", "/nonexistent/path/tmpl.txt")
	assert.Error(t, err)
}

func TestLoadTemplate_UnknownDefaultNameReturnsError(t *testing.T) {
	_, err := render.LoadTemplate("unknown-provider", "")
	assert.Error(t, err)
}

func TestLoadTemplate_AllKnownDefaultsLoad(t *testing.T) {
	for _, name := range []string{"slack", "telegram", "inapp"} {
		body, err := render.LoadTemplate(name, "")
		assert.NoError(t, err, "provider %q should have an embedded default", name)
		assert.NotEmpty(t, body, "embedded template for %q should not be empty", name)
	}
}

// ---- NewTemplateRenderer / Render ----

func TestNewTemplateRenderer_ParseError(t *testing.T) {
	_, err := render.NewTemplateRenderer("bad", "{{ .Unclosed", "text/plain", nil)
	assert.Error(t, err)
}

func TestNewTemplateRenderer_ValidTemplate(t *testing.T) {
	r, err := render.NewTemplateRenderer("hello", "hello {{ .TaskName }}", "text/plain", nil)
	require.NoError(t, err)
	assert.NotNil(t, r)
}

func TestTemplateRenderer_Render_BasicSubstitution(t *testing.T) {
	r, err := render.NewTemplateRenderer("t", "task={{ .TaskName }}", "text/plain", nil)
	require.NoError(t, err)

	ev := &notify.Event{TaskName: "backup"}
	msg, err := r.Render(ev)
	require.NoError(t, err)
	assert.Equal(t, "task=backup", string(msg.Body))
	assert.Equal(t, "text/plain", msg.ContentType)
}

func TestTemplateRenderer_Render_TitleFn(t *testing.T) {
	titleFn := func(ev *notify.Event) string { return "title:" + ev.TaskName }
	r, err := render.NewTemplateRenderer("t", "body", "text/plain", titleFn)
	require.NoError(t, err)

	ev := &notify.Event{TaskName: "job"}
	msg, err := r.Render(ev)
	require.NoError(t, err)
	assert.Equal(t, "title:job", msg.Title)
}

func TestTemplateRenderer_Render_TitleFnNil(t *testing.T) {
	r, err := render.NewTemplateRenderer("t", "body", "text/plain", nil)
	require.NoError(t, err)

	ev := &notify.Event{TaskName: "job"}
	msg, err := r.Render(ev)
	require.NoError(t, err)
	assert.Equal(t, "", msg.Title)
}

// ---- funcMap functions via template execution ----

func renderWith(t *testing.T, tmpl string, ev *notify.Event) string {
	t.Helper()
	r, err := render.NewTemplateRenderer("test", tmpl, "text/plain", nil)
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
	r, err := render.NewTemplateRenderer("test", "{{ jsonStr .Reason }}", "text/plain", nil)
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
