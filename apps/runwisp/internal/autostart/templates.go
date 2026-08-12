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
		"MaskedCronUnit": p.MaskedCronUnit,
	}); err != nil {
		return nil, err
	}
	return renderTemplate("templates/runwisp.service.tmpl", p)
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

// systemdEscape escapes the two characters that are special inside a
// systemd double-quoted string: backslash and double-quote. Control characters
// are rejected before render, so they need no handling here.
func systemdEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
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
