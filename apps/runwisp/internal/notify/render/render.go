// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// Package render produces provider-specific message bodies from notify.Event
// values via Go text/template. Defaults are embedded; per-channel overrides
// may supply a path on disk.
package render

import (
	"bytes"
	"fmt"
	"html"
	"strings"
	"text/template"
	"time"

	"github.com/runwisp/runwisp/internal/notify"
)

// RenderedMessage carries the output of a Renderer. Title and Body are the
// canonical fields; ContentType and Metadata let providers communicate
// transport details (Slack expects application/json, Telegram cares about
// parse_mode).
type RenderedMessage struct {
	ContentType string
	Body        []byte
	Subject     string
	Title       string
	Metadata    map[string]string
}

// Renderer turns a notify.Event into a provider-specific message.
type Renderer interface {
	Render(*notify.Event) (RenderedMessage, error)
}

// TemplateRenderer is the workhorse: a parsed text/template plus a content
// type label. Most providers wrap one of these.
type TemplateRenderer struct {
	tmpl        *template.Template
	contentType string
	titleFn     func(*notify.Event) string
}

// NewTemplateRenderer parses src under name and returns a Renderer using the
// shared FuncMap. titleFn produces the notification title (used by inapp and
// surfaced as Telegram preview / Slack header in defaults).
func NewTemplateRenderer(name, src, contentType string, titleFn func(*notify.Event) string) (*TemplateRenderer, error) {
	t, err := template.New(name).Funcs(funcMap()).Parse(src)
	if err != nil {
		return nil, fmt.Errorf("parse template %q: %w", name, err)
	}
	return &TemplateRenderer{tmpl: t, contentType: contentType, titleFn: titleFn}, nil
}

// Render executes the template with ev as data.
func (r *TemplateRenderer) Render(ev *notify.Event) (RenderedMessage, error) {
	var buf bytes.Buffer
	if err := r.tmpl.Execute(&buf, ev); err != nil {
		return RenderedMessage{}, fmt.Errorf("render: %w", err)
	}
	title := ""
	if r.titleFn != nil {
		title = r.titleFn(ev)
	}
	return RenderedMessage{
		ContentType: r.contentType,
		Body:        buf.Bytes(),
		Title:       title,
	}, nil
}

func funcMap() template.FuncMap {
	return template.FuncMap{
		"upper":    strings.ToUpper,
		"lower":    strings.ToLower,
		"trim":     strings.TrimSpace,
		"htmlEsc":  html.EscapeString,
		"jsonStr":  jsonEscape,
		"tgEscape": tgEscape,
		"timeRFC":  func(t time.Time) string { return t.Format(time.RFC3339) },
		"emoji":    severityEmoji,
	}
}

// jsonEscape returns a JSON-quoted string body (without surrounding quotes)
// safe for embedding inside a JSON template. Uses Go's strconv.Quote rules
// adapted for JSON: backslash, double-quote, and control bytes only.
func jsonEscape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u%04x`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}

// tgEscape escapes the four characters Telegram HTML mode requires: <, >, &,
// and ". Used by Telegram templates to safely embed user-provided text
// (task names, error messages) into <b>/<code>/<i> spans.
func tgEscape(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
	)
	return r.Replace(s)
}

func severityEmoji(s notify.Severity) string {
	switch s {
	case notify.SevError:
		return ":rotating_light:"
	case notify.SevWarn:
		return ":warning:"
	default:
		return ":information_source:"
	}
}
