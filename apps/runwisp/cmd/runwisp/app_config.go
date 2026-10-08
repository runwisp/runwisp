// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/apphost"
	"github.com/runwisp/runwisp/apps/runwisp/internal/config"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
)

// appIdleExit is how long a daemon whose config an app supplies outlives its
// last app: long enough for a restarting app or a standby copy to reconnect.
const appIdleExit = 10 * time.Second

// appFlagUsage documents `--app` on every command that takes it.
const appFlagUsage = "read the config as a JSON document on stdin instead of from --config, which only anchors its relative paths (for apps that define their config in code)"

// configSource is where the config comes from: CfgFile, or the document
// `--app` read in its place.
func configSource(f Flags) config.Source {
	if f.ConfigDoc != nil {
		return config.NewDocumentSource(f.CfgFile, f.ConfigDoc)
	}
	return config.FileSource(f.CfgFile)
}

// withAppDocument reads the config document `--app` takes on stdin into f;
// without `--app` (enabled false) it returns f unchanged.
func withAppDocument(f Flags, in io.Reader, enabled bool) (Flags, error) {
	if !enabled {
		return f, nil
	}
	doc, err := io.ReadAll(in)
	if err != nil {
		return f, fmt.Errorf("read the config document from stdin: %w", err)
	}
	if len(bytes.TrimSpace(doc)) == 0 {
		return f, errors.New("--app reads the config as a JSON document on stdin, and stdin was empty")
	}
	f.ConfigDoc = doc
	return f, nil
}

// serveAppConfig makes the connected app the config's source of truth: its
// documents reload the daemon, and the daemon exits once no copy of the app
// has been connected for appIdleExit.
func serveAppConfig(apps *apphost.Host, doc *config.DocumentSource, reload func() (model.ReloadResult, error)) {
	apps.AcceptConfig(doc, reload)
	apps.ExitWhenIdle(appIdleExit, func() {
		slog.Info("The app is gone; shutting down")
		if err := signalSelfTerm(); err != nil {
			slog.Error("Failed to shut down after the app left", "err", err)
		}
	})
}
