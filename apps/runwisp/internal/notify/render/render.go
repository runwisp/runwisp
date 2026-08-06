// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package render produces provider-specific message bodies from notify.Event
// values via Go text/template. Defaults are embedded; per-channel overrides
// may supply a path on disk.
package render

import (
	"bytes"
	"fmt"
	"html"
	"net/url"
	"strings"
	"text/template"
	"time"

	"github.com/runwisp/runwisp/internal/model"
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

// TemplateContext carries per-daemon values that need to bind into the
// template's func map at construction time so individual channels don't
// have to know about base URLs, fingerprints, or how to read log files.
//
// All fields are optional. Zero values produce safe defaults: empty
// ExternalURL suppresses run-link rendering, empty Fingerprint omits the
// footer token, nil OutputTail returns the empty string (the template branch
// that wraps it then collapses).
type TemplateContext struct {
	ExternalURL string
	Fingerprint string
	OutputTail  func(logPath string, maxLines, maxBytes int) string
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
// surfaced as Telegram preview / Slack header in defaults). The in-app
// renderer uses this form — it needs no external URL, fingerprint, or tail
// reader.
func NewTemplateRenderer(name, src, contentType string, titleFn func(*notify.Event) string) (*TemplateRenderer, error) {
	return NewTemplateRendererWithContext(name, src, contentType, titleFn, TemplateContext{})
}

// NewTemplateRendererWithContext is like NewTemplateRenderer but binds the
// given TemplateContext into the funcMap closures, exposing per-daemon
// helpers (runURL, taskURL, outputTail, fingerprint) to the template.
func NewTemplateRendererWithContext(name, src, contentType string, titleFn func(*notify.Event) string, ctx TemplateContext) (*TemplateRenderer, error) {
	t, err := template.New(name).Funcs(funcMap(ctx)).Parse(src)
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

func funcMap(ctx TemplateContext) template.FuncMap {
	return template.FuncMap{
		"upper":         strings.ToUpper,
		"lower":         strings.ToLower,
		"trim":          strings.TrimSpace,
		"htmlEsc":       html.EscapeString,
		"jsonStr":       jsonEscape,
		"tgEscape":      tgEscape,
		"timeRFC":       func(t time.Time) string { return t.Format(time.RFC3339) },
		"emoji":         severityEmoji,
		"statusEmoji":   statusEmoji,
		"statusVerb":    statusVerb,
		"humanTime":     humanTime,
		"humanDuration": humanDuration,
		"runDuration":   runDuration,
		"triggerPhrase": triggerPhrase,
		"eventSentence": eventSentence,
		"eventTrigger":  eventTrigger,
		"linkLabel":     linkLabel,
		"runURL":        func(r *model.Run) string { return runURL(ctx.ExternalURL, r) },
		"taskURL":       func(name string) string { return taskURL(ctx.ExternalURL, name) },
		"outputTail": func(ev *notify.Event) string {
			if ctx.OutputTail == nil || ev == nil {
				return ""
			}
			return ctx.OutputTail(ev.LogPath, 3, 300)
		},
		"fingerprint": func() string { return ctx.Fingerprint },
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

// statusEmoji returns the per-kind emoji used in headlines.
func statusEmoji(k notify.Kind) string {
	switch k {
	case notify.KindRunFailed:
		return "❌"
	case notify.KindRunSucceeded:
		return "✅"
	case notify.KindRunTimeout:
		return "⏱️"
	case notify.KindRunStopped:
		return "⏹️"
	case notify.KindRunCrashed:
		return "💥"
	case notify.KindRunMissed:
		return "🕳️"
	case notify.KindServiceFatal:
		return "🛑"
	case notify.KindRunStarted:
		return "▶️"
	case notify.KindLogDiskPressure:
		return "💾"
	case notify.KindNotifyDeliveryFailed:
		return "⚠️"
	default:
		return ""
	}
}

// statusVerb returns the past-tense verb that completes the headline
// "<task> <verb>". Phrasing mirrors notify.Kind.Title.
func statusVerb(k notify.Kind) string {
	switch k {
	case notify.KindRunFailed:
		return "failed"
	case notify.KindRunSucceeded:
		return "succeeded"
	case notify.KindRunTimeout:
		return "timed out"
	case notify.KindRunStopped:
		return "was stopped"
	case notify.KindRunCrashed:
		return "crashed"
	case notify.KindRunMissed:
		return "missed a scheduled run"
	case notify.KindServiceFatal:
		return "gave up restarting"
	case notify.KindRunStarted:
		return "started"
	case notify.KindLogDiskPressure:
		return "log output paused"
	case notify.KindNotifyDeliveryFailed:
		return "delivery failed"
	default:
		return string(k)
	}
}

// humanTime renders a timestamp in the operator-facing "2 Jan, 15:04" shape.
// Uses the time's own location, which is the daemon's scheduler TZ for
// events emitted via the bridge.
func humanTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2 Jan, 15:04")
}

// humanDuration formats a duration the way an operator would read aloud:
// "0.3s", "12s", "3m 4s", "1h 12m". Negative or zero durations yield "0s"
// so the renderer never emits a bare "-0.0s" eyesore.
func humanDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	if d < time.Second {
		seconds := float64(d) / float64(time.Second)
		return fmt.Sprintf("%.1fs", seconds)
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Round(time.Second)/time.Second))
	}
	if d < time.Hour {
		m := int(d / time.Minute)
		s := int((d - time.Duration(m)*time.Minute).Round(time.Second) / time.Second)
		if s == 0 {
			return fmt.Sprintf("%dm", m)
		}
		return fmt.Sprintf("%dm %ds", m, s)
	}
	h := int(d / time.Hour)
	m := int((d - time.Duration(h)*time.Hour) / time.Minute)
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh %dm", h, m)
}

// runDuration derives a humanized duration from run.EndAt - run.StartAt.
// Returns the empty string when either endpoint is missing — the template
// then omits the duration phrase entirely rather than printing "0s".
func runDuration(r *model.Run) string {
	if r == nil || r.StartAt == nil || r.EndAt == nil {
		return ""
	}
	return humanDuration(r.EndAt.Sub(*r.StartAt))
}

// triggerPhrase maps a TriggeredBy to the operator-facing sentence prefix
// used in notification bodies. Unknown values fall through to the literal
// enum value so adding new triggers doesn't silently lose context.
func triggerPhrase(t model.TriggeredBy) string {
	switch t {
	case model.TriggeredByCron:
		return "Scheduled run"
	case model.TriggeredByAPI:
		return "Manually triggered via API"
	case model.TriggeredByCloud:
		return "Triggered from the control plane"
	case model.TriggeredByService:
		return "Service auto-started"
	case model.TriggeredByStartup:
		return "Ran on daemon startup"
	default:
		if t == "" {
			return "Run"
		}
		return string(t)
	}
}

// eventSentence renders the per-kind body sentence ending in a period. It is
// the single source of truth for the failure/success phrasing previously
// duplicated inside the Telegram and Slack templates. Output is plain text;
// callers apply the provider-specific escape (tgEscape, jsonStr).
func eventSentence(e *notify.Event) string {
	switch e.Kind {
	case notify.KindRunFailed:
		return failedSentence(e)
	case notify.KindRunSucceeded:
		return succeededSentence(e)
	case notify.KindRunTimeout:
		return timeoutSentence(e)
	case notify.KindRunStopped:
		return stoppedSentence(e)
	case notify.KindRunCrashed:
		return crashedSentence(e)
	case notify.KindRunMissed:
		return missedSentence(e)
	case notify.KindRunStarted:
		return "Run started."
	case notify.KindLogDiskPressure:
		return "Disk pressure is high; log capture is paused for this task until disk space is recovered."
	case notify.KindNotifyDeliveryFailed:
		return deliveryFailedSentence(e)
	default:
		return defaultSentence(e)
	}
}

// failedSentence reports the exit code (and duration when known) for a failed run.
func failedSentence(e *notify.Event) string {
	code := "?"
	if e.Run != nil {
		code = fmt.Sprintf("%d", e.Run.ExitCode)
	}
	if d := runDuration(e.Run); d != "" {
		return fmt.Sprintf("Exited with code %s after %s.", code, d)
	}
	return fmt.Sprintf("Exited with code %s.", code)
}

// succeededSentence reports a successful completion, including duration when known.
func succeededSentence(e *notify.Event) string {
	if d := runDuration(e.Run); d != "" {
		return fmt.Sprintf("Completed in %s.", d)
	}
	return "Completed."
}

// timeoutSentence reports a run killed by the configured timeout.
func timeoutSentence(e *notify.Event) string {
	if d := runDuration(e.Run); d != "" {
		return fmt.Sprintf("The task was killed after the configured timeout (%s elapsed).", d)
	}
	return "The task was killed after the configured timeout."
}

// stoppedSentence reports a manually stopped run, including duration when known.
func stoppedSentence(e *notify.Event) string {
	if d := runDuration(e.Run); d != "" {
		return fmt.Sprintf("Stopped manually after %s.", d)
	}
	return "Stopped manually."
}

// crashedSentence reports a process that couldn't start, including the reason when present.
func crashedSentence(e *notify.Event) string {
	if e.Reason != "" {
		return fmt.Sprintf("The process couldn't start: %s.", e.Reason)
	}
	return "The process couldn't start."
}

// missedSentence renders missed-run phrasing. The reason already reads as a full
// sentence built at detection time (count + window + optional cap note), so it is
// rendered verbatim.
func missedSentence(e *notify.Event) string {
	if e.Reason != "" {
		return e.Reason
	}
	return "A scheduled run was missed while the daemon was down."
}

// deliveryFailedSentence reports a failed notification delivery, including the reason when present.
func deliveryFailedSentence(e *notify.Event) string {
	if e.Reason != "" {
		return fmt.Sprintf("A notification could not be delivered: %s.", e.Reason)
	}
	return "A notification could not be delivered."
}

// defaultSentence is the fallback for kinds without bespoke phrasing: the reason
// verbatim when present, otherwise the kind's status verb as a sentence.
func defaultSentence(e *notify.Event) string {
	if e.Reason != "" {
		return e.Reason
	}
	return statusVerb(e.Kind) + "."
}

// eventTrigger returns just the trigger phrase ("Scheduled run", "Manually
// triggered via API", or "Event" fallback when the event carries no Run).
// The template owns the separator, timestamp, and trailing period.
func eventTrigger(e *notify.Event) string {
	if e.Run == nil {
		return "Event"
	}
	return triggerPhrase(e.Run.TriggeredBy)
}

// linkLabel returns the call-to-action label for the run/task link rendered
// at the bottom of a notification.
func linkLabel(k notify.Kind) string {
	switch k {
	case notify.KindRunFailed:
		return "View full run"
	case notify.KindLogDiskPressure:
		return "Open task"
	default:
		return "View run"
	}
}

// runURL builds the deep-link to a specific run in the embedded dashboard.
// Returns "" when the operator hasn't configured external_url or when the
// run/task name is missing — callers wrap the result in a template
// conditional so the link line vanishes cleanly rather than rendering an
// orphan anchor.
func runURL(externalURL string, r *model.Run) string {
	if externalURL == "" || r == nil || r.TaskName == "" || r.ID == "" {
		return ""
	}
	return fmt.Sprintf("%s/tasks/%s/%s", externalURL, url.PathEscape(r.TaskName), url.PathEscape(r.ID))
}

// taskURL is the link to a task's dashboard page, used by log.disk_pressure
// where no run identifier exists.
func taskURL(externalURL, taskName string) string {
	if externalURL == "" || taskName == "" {
		return ""
	}
	return fmt.Sprintf("%s/tasks/%s", externalURL, url.PathEscape(taskName))
}
