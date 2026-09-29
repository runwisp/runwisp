// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package autostart

import (
	"bytes"
	"embed"
	"encoding/xml"
	"fmt"
	"strings"
	"text/template"
)

//go:embed templates/*.tmpl
var templatesFS embed.FS

// SystemdParams is the data passed to runwisp.service.tmpl.
type SystemdParams struct {
	Binary     string
	Config     string
	DataDir    string
	Host       string
	Port       int
	Home       string
	Path       string
	ConfigHash string
	BinarySHA  string
	// System selects the system-wide unit shape: WantedBy=multi-user.target
	// instead of default.target, and the extra After= ordering a system
	// daemon needs (remote-fs.target / nss-user-lookup.target for a config
	// or account database that may live on a network mount or NSS backend,
	// time-sync.target so a boot-time clock step lands before scheduling
	// starts on an RTC-less box).
	System bool
	// MaskedCronUnit, when non-empty, is recorded as a
	// # runwisp-masked-cron: <unit> marker line — the standing record of
	// which cron unit this install has masked, so uninstall knows it may
	// unmask it and Status can show the post-cutover check.
	MaskedCronUnit string
	// CronPriorState records, as a # runwisp-cron-prior-state marker, what
	// MaskedCronUnit looked like before the take-over first touched it
	// (cronPriorActive/Inactive/Masked), so undo restores exactly that.
	CronPriorState string
	// CronFailsafeUnit, when non-empty, caps restarts and names the unit
	// OnFailure= starts once systemd gives up on RunWisp, so a crash-looping
	// daemon hands the jobs back to the cron it masked.
	CronFailsafeUnit string
}

// CronFailsafeParams is the data passed to runwisp-cron-failsafe.service.tmpl.
type CronFailsafeParams struct {
	// Service is the RunWisp unit whose failure starts the failsafe.
	Service string
	// CronUnit is the cron unit the take-over masked.
	CronUnit string
}

// LaunchdParams is the data passed to com.runwisp.daemon.plist.tmpl.
type LaunchdParams struct {
	Binary     string
	Config     string
	DataDir    string
	Host       string
	Port       int
	Home       string
	Path       string
	LogPath    string
	ConfigHash string
	BinarySHA  string
	// Label is the per-instance launchd label baked into the plist
	// (e.g. "com.runwisp.daemon.bright-falcon").
	Label string
}

// RenderSystemdUnit returns the rendered runwisp.service body. Every
// operator-supplied field is validated free of control characters (a newline
// in Binary/Config/Host/etc. would otherwise inject additional [Service]
// directives — including a replacement ExecStart — and the unit is written and
// started as root, so that is a root-RCE primitive) and each value is
// systemd-quoted at its interpolation site.
func RenderSystemdUnit(p SystemdParams) ([]byte, error) {
	if err := rejectControlChars(map[string]string{
		"Binary": p.Binary, "Config": p.Config, "DataDir": p.DataDir,
		"Host": p.Host, "Home": p.Home, "Path": p.Path,
		"ConfigHash": p.ConfigHash, "BinarySHA": p.BinarySHA,
		"MaskedCronUnit": p.MaskedCronUnit, "CronPriorState": p.CronPriorState,
		"CronFailsafeUnit": p.CronFailsafeUnit,
	}); err != nil {
		return nil, err
	}
	return renderTemplate("templates/runwisp.service.tmpl", p)
}

// RenderCronFailsafeUnit returns the oneshot unit a take-over installs next to
// runwisp.service. Both names come from this package (serviceName and
// importer.CronUnits), never from an operator, so they are interpolated as-is.
func RenderCronFailsafeUnit(p CronFailsafeParams) ([]byte, error) {
	if err := rejectControlChars(map[string]string{"Service": p.Service, "CronUnit": p.CronUnit}); err != nil {
		return nil, err
	}
	return renderTemplate("templates/runwisp-cron-failsafe.service.tmpl", p)
}

// RenderLaunchdPlist returns the rendered com.runwisp.daemon.plist body. As with
// the systemd unit, control characters are rejected up front and every value is
// XML-escaped at its interpolation site so a value cannot break out of its
// <string> element and inject plist structure.
func RenderLaunchdPlist(p LaunchdParams) ([]byte, error) {
	if err := rejectControlChars(map[string]string{
		"Binary": p.Binary, "Config": p.Config, "DataDir": p.DataDir,
		"Host": p.Host, "Home": p.Home, "Path": p.Path, "LogPath": p.LogPath,
		"ConfigHash": p.ConfigHash, "BinarySHA": p.BinarySHA, "Label": p.Label,
	}); err != nil {
		return nil, err
	}
	return renderTemplate("templates/com.runwisp.daemon.plist.tmpl", p)
}

// templateFuncs escape interpolated values for their target format.
//   - sysq:   systemd double-quoted argument (ExecStart tokens)
//   - sysesc: systemd escape without wrapping (inside Environment="KEY=…")
//   - xml:    XML text/attribute escaping (launchd <string> bodies)
var templateFuncs = template.FuncMap{
	"sysesc": systemdEscape,
	"sysq":   func(s string) string { return `"` + systemdEscape(s) + `"` },
	"xml":    xmlEscape,
}

// systemdEscape escapes the characters that are special inside a systemd
// unit value: backslash and double-quote (special inside a double-quoted
// string) and "%" (systemd's specifier-expansion prefix — "%%" is required for
// a literal percent, or a value like RUNWISP_PASSWORD containing "%h" would
// silently expand to the user's home directory). Control characters are
// rejected before render, so they need no handling here.
func systemdEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, `%`, `%%`).Replace(s)
}

func xmlEscape(s string) string {
	var buf bytes.Buffer
	if err := xml.EscapeText(&buf, []byte(s)); err != nil {
		return ""
	}
	return buf.String()
}

// rejectControlChars fails if any value contains an ASCII control character
// (< 0x20, or DEL 0x7f). These are the characters that let a value escape its
// line/element and inject new directives; a filesystem path or hostname never
// legitimately contains one.
func rejectControlChars(fields map[string]string) error {
	for name, val := range fields {
		for i := 0; i < len(val); i++ {
			if c := val[i]; c < 0x20 || c == 0x7f {
				return fmt.Errorf("service unit field %s contains a control character (0x%02x); refusing to write unit", name, c)
			}
		}
	}
	return nil
}

func renderTemplate(name string, data any) ([]byte, error) {
	raw, err := templatesFS.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("read template %s: %w", name, err)
	}
	tpl, err := template.New(name).Funcs(templateFuncs).Parse(string(raw))
	if err != nil {
		return nil, fmt.Errorf("parse template %s: %w", name, err)
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("execute template %s: %w", name, err)
	}
	return buf.Bytes(), nil
}
