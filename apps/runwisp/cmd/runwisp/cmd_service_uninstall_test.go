// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// `service uninstall` said nothing about the tasks and services only RunWisp
// ran, and left promoted crontab lines commented out, so a nightly backup never
// ran again. It now lists them, and hands the promoted lines back to cron.
func TestUninstallImpact_ListsWhatStopsAndRestoresPromotedLines(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "crontabs"), 0o755))
	crontab := filepath.Join(dir, "crontabs", "root")
	require.NoError(t, os.WriteFile(crontab, []byte(
		"# runwisp: this job was promoted to "+filepath.Join(dir, "runwisp.toml")+"\n"+
			"#15 3 * * * /srv/app/backup.sh\n"+
			"*/5 * * * * /usr/bin/curl -fsS http://127.0.0.1:8080/health\n"), 0o644))
	cfgPath := filepath.Join(dir, "runwisp.toml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(`[daemon]
include_cron = ["crontabs/*"]

[tasks.backup]
run = "/srv/app/backup.sh"
cron = "15 3 * * *"

[services.web]
run = "/srv/app/web"
`), 0o644))

	imp := computeUninstallImpact(cfgPath)
	var out bytes.Buffer
	imp.print(&out)
	assert.Contains(t, out.String(), "services: web")
	assert.Contains(t, out.String(), "tasks:    backup")
	assert.NotContains(t, out.String(), "curl", "cron runs its own jobs again")
	assert.Contains(t, out.String(), crontab+":1  15 3 * * * /srv/app/backup.sh")

	imp.restoreCron(&out)
	got, err := os.ReadFile(crontab)
	require.NoError(t, err)
	assert.Equal(t, "15 3 * * * /srv/app/backup.sh\n*/5 * * * * /usr/bin/curl -fsS http://127.0.0.1:8080/health\n", string(got))
	assert.Contains(t, out.String(), "Restored 1 promoted crontab line.")
}
