// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package autostart

import (
	"bytes"
	"embed"
	"fmt"
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

// RenderSystemdUnit returns the rendered runwisp.service body.
func RenderSystemdUnit(p SystemdParams) ([]byte, error) {
	return renderTemplate("templates/runwisp.service.tmpl", p)
}

// RenderLaunchdPlist returns the rendered com.runwisp.daemon.plist body.
func RenderLaunchdPlist(p LaunchdParams) ([]byte, error) {
	return renderTemplate("templates/com.runwisp.daemon.plist.tmpl", p)
}

func renderTemplate(name string, data any) ([]byte, error) {
	raw, err := templatesFS.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("read template %s: %w", name, err)
	}
	tpl, err := template.New(name).Parse(string(raw))
	if err != nil {
		return nil, fmt.Errorf("parse template %s: %w", name, err)
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("execute template %s: %w", name, err)
	}
	return buf.Bytes(), nil
}
