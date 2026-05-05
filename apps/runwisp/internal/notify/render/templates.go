// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

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
	switch ev.Kind {
	case notify.KindRunStarted:
		return fmt.Sprintf("%s started", ev.TaskName)
	case notify.KindRunSucceeded:
		return fmt.Sprintf("%s succeeded", ev.TaskName)
	case notify.KindRunFailed:
		return fmt.Sprintf("%s failed", ev.TaskName)
	case notify.KindRunTimeout:
		return fmt.Sprintf("%s timed out", ev.TaskName)
	case notify.KindRunStopped:
		return fmt.Sprintf("%s stopped", ev.TaskName)
	case notify.KindRunCrashed:
		return fmt.Sprintf("%s crashed", ev.TaskName)
	case notify.KindNotifyDeliveryFailed:
		channel := ""
		if ev.Extra != nil {
			if v, ok := ev.Extra["channel"].(string); ok {
				channel = v
			}
		}
		if channel != "" {
			return fmt.Sprintf("Delivery to %s failed", channel)
		}
		return "Notification delivery failed"
	default:
		return string(ev.Kind)
	}
}
