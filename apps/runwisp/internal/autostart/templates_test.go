// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package autostart

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderSystemdUnit_Golden(t *testing.T) {
	body, err := RenderSystemdUnit(SystemdParams{
		Binary:     "/home/alice/.local/bin/runwisp",
		Config:     "/home/alice/.config/runwisp/runwisp.toml",
		DataDir:    "/home/alice/.local/share/runwisp",
		Host:       "127.0.0.1",
		Port:       9477,
		Home:       "/home/alice",
		Path:       "/usr/local/bin:/usr/bin:/bin",
		ConfigHash: "deadbeef0000",
		BinarySHA:  "abcdef012345",
	})
	require.NoError(t, err)

	want := strings.Join([]string{
		"# Managed by runwisp service install — DO NOT EDIT",
		"# runwisp-config-hash: deadbeef0000",
		"# runwisp-binary-sha256: abcdef012345",
		"[Unit]",
		"Description=RunWisp — lightweight cron daemon",
		"Documentation=https://docs.runwisp.com/",
		"After=network.target",
		"",
		"[Service]",
		"Type=simple",
		"ExecStart=/home/alice/.local/bin/runwisp daemon --config /home/alice/.config/runwisp/runwisp.toml --data /home/alice/.local/share/runwisp --port 9477 --host 127.0.0.1",
		"Restart=on-failure",
		"RestartSec=5s",
		"KillMode=mixed",
		"TimeoutStopSec=30s",
		"Environment=HOME=/home/alice",
		"Environment=PATH=/usr/local/bin:/usr/bin:/bin",
		"Environment=LANG=C.UTF-8",
		"Environment=RUNWISP_SERVICE_MANAGED=1",
		"",
		"[Install]",
		"WantedBy=default.target",
		"",
	}, "\n")
	assert.Equal(t, want, string(body))
}

func TestRenderLaunchdPlist_Golden(t *testing.T) {
	body, err := RenderLaunchdPlist(LaunchdParams{
		Binary:     "/Users/alice/.local/bin/runwisp",
		Config:     "/Users/alice/.config/runwisp/runwisp.toml",
		DataDir:    "/Users/alice/Library/Application Support/runwisp",
		Host:       "127.0.0.1",
		Port:       9477,
		Home:       "/Users/alice",
		Path:       "/usr/local/bin:/usr/bin:/bin",
		LogPath:    "/Users/alice/Library/Application Support/runwisp/daemon.log",
		ConfigHash: "deadbeef0000",
		BinarySHA:  "abcdef012345",
		Label:      "com.runwisp.daemon.bright-falcon",
	})
	require.NoError(t, err)
	s := string(body)

	assert.Contains(t, s, "<?xml version=\"1.0\" encoding=\"UTF-8\"?>")
	assert.Contains(t, s, "<!-- "+managedMarkerBare+" -->")
	assert.Contains(t, s, "<key>Label</key>")
	assert.Contains(t, s, "<string>com.runwisp.daemon.bright-falcon</string>")
	assert.Contains(t, s, "<string>/Users/alice/.local/bin/runwisp</string>")
	assert.Contains(t, s, "<string>daemon</string>")
	assert.Contains(t, s, "<string>--config</string>")
	assert.Contains(t, s, "<key>RunAtLoad</key>")
	assert.Contains(t, s, "<key>KeepAlive</key>")
	// KeepAlive on launchd is the analog of Restart=on-failure;
	// we use the SuccessfulExit=false variant.
	assert.Contains(t, s, "<key>SuccessfulExit</key>")
	assert.Contains(t, s, "<key>StandardOutPath</key>")
	assert.Contains(t, s, "<key>EnvironmentVariables</key>")
	assert.Contains(t, s, "<key>RUNWISP_SERVICE_MANAGED</key>")

	// Marker round-trip: extractMarkers must recognise the file
	// we just rendered.
	parsed := extractMarkers(body)
	assert.True(t, parsed.managed)
	assert.Equal(t, "deadbeef0000", parsed.configHash)
	assert.Equal(t, "abcdef012345", parsed.binarySHA)
}

func TestRenderSystemdUnit_RoundTripsManagedMarker(t *testing.T) {
	body, err := RenderSystemdUnit(SystemdParams{
		Binary:     "/usr/bin/runwisp",
		Config:     "/etc/runwisp/runwisp.toml",
		DataDir:    "/var/lib/runwisp",
		Host:       "127.0.0.1",
		Port:       9477,
		Home:       "/home/alice",
		Path:       "/usr/bin",
		ConfigHash: "0011223344ff",
		BinarySHA:  "ffeeddccbbaa",
	})
	require.NoError(t, err)
	parsed := extractMarkers(body)
	assert.True(t, parsed.managed)
	assert.Equal(t, "0011223344ff", parsed.configHash)
	assert.Equal(t, "ffeeddccbbaa", parsed.binarySHA)
}
