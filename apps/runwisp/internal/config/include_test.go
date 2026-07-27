// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeFileTree stages files (keys may contain subdirs) under a fresh temp dir
// and returns the dir. The root config is conventionally "runwisp.toml".
func writeFileTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		full := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0o600))
	}
	return dir
}

func TestInclude_AccumulatesTasksNotifiersRoutes(t *testing.T) {
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml": `
[daemon]
include = ["conf.d/*.toml"]

[tasks.root_task]
run = "echo root"

[[notifier]]
id = "root-slack"
type = "slack"
webhook_url = "https://example.com/root"
`,
		"conf.d/a.toml": `
[tasks.a_task]
run = "echo a"

[[notifier]]
id = "a-slack"
type = "slack"
webhook_url = "https://example.com/a"

[[notification_route]]
match = { task = "a_task" }
notify = ["a-slack"]
`,
		"conf.d/b.toml": `
[services.b_svc]
run = "sleep 1"
`,
	})
	cfg, err := Load(filepath.Join(dir, "runwisp.toml"))
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{"root_task", "a_task", "b_svc"}, taskNames(cfg))

	var notifierIDs []string
	for _, n := range cfg.Notify.Notifiers {
		notifierIDs = append(notifierIDs, n.ID)
	}
	assert.Subset(t, notifierIDs, []string{"root-slack", "a-slack"})
}

func TestInclude_RelativePathsResolveAgainstIncludedFileDir(t *testing.T) {
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml": `
[daemon]
include = ["conf.d/svc.toml"]
`,
		"conf.d/svc.toml": `
[tasks.worker]
run = "echo hi"
env_file = "worker.env"
working_dir = "data"
`,
		"conf.d/worker.env": "TOKEN=fromconf\n",
	})
	cfg, err := Load(filepath.Join(dir, "runwisp.toml"))
	require.NoError(t, err)

	worker := findTask(t, cfg, "worker")
	// env_file resolves against conf.d/, not the root dir.
	assert.Equal(t, map[string]string{"TOKEN": "fromconf"}, worker.Env)
	// working_dir resolves to an absolute path under conf.d/.
	assert.Equal(t, filepath.Join(dir, "conf.d", "data"), worker.WorkingDir)
	// The as-written string is preserved for the API/UI.
	assert.Equal(t, "worker.env", worker.EnvFile)
}

func TestInclude_FileSubstitutionResolvesPerFile(t *testing.T) {
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml": `
[daemon]
include = ["conf.d/svc.toml"]
`,
		"conf.d/svc.toml": `
[tasks.worker]
run = "echo hi"
env = { TOKEN = "${file:token.txt}" }
`,
		"conf.d/token.txt": "secret-value\n",
	})
	cfg, err := Load(filepath.Join(dir, "runwisp.toml"))
	require.NoError(t, err)

	worker := findTask(t, cfg, "worker")
	assert.Equal(t, "secret-value", worker.Env["TOKEN"])
}

func TestInclude_GlobIsSortedAndDeterministic(t *testing.T) {
	// Two files each define a task; load must succeed regardless of FS order.
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml":   "[daemon]\ninclude = [\"conf.d/*.toml\"]\n",
		"conf.d/01.toml": "[tasks.first]\nrun = \"echo 1\"\n",
		"conf.d/02.toml": "[tasks.second]\nrun = \"echo 2\"\n",
	})
	cfg, err := Load(filepath.Join(dir, "runwisp.toml"))
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"first", "second"}, taskNames(cfg))
	assert.Len(t, cfg.includeFiles, 2)
	assert.Equal(t, []string{
		filepath.Join(dir, "conf.d", "01.toml"),
		filepath.Join(dir, "conf.d", "02.toml"),
	}, cfg.includeFiles)
}

func TestInclude_AbsolutePattern(t *testing.T) {
	dir := writeFileTree(t, map[string]string{
		"extra/x.toml": "[tasks.x]\nrun = \"echo x\"\n",
	})
	root := filepath.Join(dir, "runwisp.toml")
	require.NoError(t, os.WriteFile(root,
		[]byte("[daemon]\ninclude = [\""+filepath.Join(dir, "extra", "*.toml")+"\"]\n"), 0o600))
	cfg, err := Load(root)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"x"}, taskNames(cfg))
}

func TestInclude_DuplicateTaskNameAcrossFilesIsError(t *testing.T) {
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml": `
[daemon]
include = ["conf.d/*.toml"]

[tasks.dup]
run = "echo root"
`,
		"conf.d/a.toml": "[tasks.dup]\nrun = \"echo a\"\n",
	})
	_, err := Load(filepath.Join(dir, "runwisp.toml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dup")
	assert.Contains(t, err.Error(), "runwisp.toml")
	assert.Contains(t, err.Error(), filepath.Join("conf.d", "a.toml"))
}

func TestInclude_ServiceTaskCollisionAcrossFilesIsError(t *testing.T) {
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml": `
[daemon]
include = ["conf.d/*.toml"]

[tasks.shared]
run = "echo root"
`,
		"conf.d/a.toml": "[services.shared]\nrun = \"sleep 1\"\n",
	})
	_, err := Load(filepath.Join(dir, "runwisp.toml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "shared")
}

func TestInclude_ComposeAliasCollisionAcrossFilesIsError(t *testing.T) {
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml": `
[daemon]
include = ["conf.d/*.toml"]

[tasks.myapp]
run = "echo root"
`,
		"conf.d/a.toml": "[compose.myapp]\nfile = \"docker-compose.yml\"\n",
	})
	_, err := Load(filepath.Join(dir, "runwisp.toml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "myapp")
}

func TestInclude_SingletonInIncludedFileIsError(t *testing.T) {
	cases := map[string]string{
		"defaults":  "[defaults]\nshell = \"/bin/bash\"\n",
		"scheduler": "[scheduler]\ntimezone = \"UTC\"\n",
		"storage":   "[storage]\nmax_size = \"1gb\"\n",
		"notify":    "[notify]\nhistory_keep = 5\n",
		"daemon":    "[daemon]\nexternal_url = \"https://example.com\"\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			dir := writeFileTree(t, map[string]string{
				"runwisp.toml":  "[daemon]\ninclude = [\"conf.d/*.toml\"]\n",
				"conf.d/a.toml": body,
			})
			_, err := Load(filepath.Join(dir, "runwisp.toml"))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "root config")
		})
	}
}

func TestInclude_NestedIncludeIsError(t *testing.T) {
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml":  "[daemon]\ninclude = [\"conf.d/*.toml\"]\n",
		"conf.d/a.toml": "[daemon]\ninclude = [\"more/*.toml\"]\n",
	})
	_, err := Load(filepath.Join(dir, "runwisp.toml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nested")
}

func TestInclude_ComposeBlockInIncludedFileResolvesPaths(t *testing.T) {
	composeYML := "services:\n  web:\n    image: nginx:alpine\n"
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml":              "[daemon]\ninclude = [\"conf.d/*.toml\"]\n",
		"conf.d/app.toml":           "[compose.myapp]\nfile = \"docker-compose.yml\"\n",
		"conf.d/docker-compose.yml": composeYML,
	})
	cfg, err := Load(filepath.Join(dir, "runwisp.toml"))
	require.NoError(t, err)

	web := findTask(t, cfg, "myapp.web")
	ce, ok := web.ExecutionDef.(*model.ComposeExecution)
	require.True(t, ok)
	// The compose file path resolves against conf.d/, where it lives.
	assert.Equal(t, filepath.Join(dir, "conf.d", "docker-compose.yml"), ce.File)
}

func TestInclude_NoMatchesIsNotAnError(t *testing.T) {
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml": "[daemon]\ninclude = [\"conf.d/*.toml\"]\n\n[tasks.solo]\nrun = \"echo hi\"\n",
	})
	cfg, err := Load(filepath.Join(dir, "runwisp.toml"))
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"solo"}, taskNames(cfg))
	assert.Empty(t, cfg.includeFiles)
}
