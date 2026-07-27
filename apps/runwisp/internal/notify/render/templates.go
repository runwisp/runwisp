// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package render

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/runwisp/runwisp/internal/notify"
)

//go:embed defaults/*
var defaults embed.FS

// LoadDefaultTemplate returns the embedded template body for the given name
// (e.g. "slack", "telegram", "inapp").
func LoadDefaultTemplate(name string) (string, error) {
	for _, ext := range []string{".tmpl.json", ".tmpl.html", ".tmpl.txt"} {
		path := filepath.ToSlash(filepath.Join("defaults", name+ext))
		b, err := defaults.ReadFile(path)
		if err == nil {
			return string(b), nil
		}
	}
	return "", fmt.Errorf("no embedded template for %q", name)
}

// LoadTemplate returns the body of a template, preferring an on-disk override
// at userPath when non-empty and falling back to the embedded default.
func LoadTemplate(name, userPath string) (string, error) {
	if userPath = strings.TrimSpace(userPath); userPath != "" {
		b, err := os.ReadFile(userPath)
		if err != nil {
			return "", fmt.Errorf("read template %s: %w", userPath, err)
		}
		return string(b), nil
	}
	return LoadDefaultTemplate(name)
}

// DefaultTitle is the in-app title formatter shared across providers.
func DefaultTitle(ev *notify.Event) string {
	if ev == nil {
		return ""
	}
	return ev.Kind.Title(ev)
}
