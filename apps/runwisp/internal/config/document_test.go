// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// realConfigs is every runwisp.toml the repo ships or generates: the demo
// config and the importer goldens.
func realConfigs(t *testing.T) []string {
	t.Helper()
	fixtures := []string{"../demo/runwisp.toml"}
	for _, pattern := range []string{"../importer/testdata/*/*.golden.toml", "../importer/testdata/*/*/*.golden.toml"} {
		matches, err := filepath.Glob(pattern)
		require.NoError(t, err)
		fixtures = append(fixtures, matches...)
	}
	return fixtures
}

// tomlFileAsJSON is the JSON spelling of a runwisp.toml, what an app would
// send for the same config.
func tomlFileAsJSON(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var decoded any
	require.NoError(t, toml.Unmarshal(raw, &decoded))
	doc, err := json.Marshal(decoded)
	require.NoError(t, err)
	return doc
}

// A config sent as a JSON document must load into exactly what the same
// config loads into from runwisp.toml: same tasks, defaults, notifiers,
// routes, and the same errors. This is what keeps the SDK at parity with TOML.
func TestLoadDocumentMatchesLoad(t *testing.T) {
	for _, path := range realConfigs(t) {
		t.Run(path, func(t *testing.T) {
			fromFile, fileErr := Load(path)
			fromDoc, docErr := LoadDocument(path, tomlFileAsJSON(t, path))
			if fileErr != nil {
				require.Error(t, docErr)
				assert.Equal(t, fileErr.Error(), docErr.Error())
				return
			}
			require.NoError(t, docErr)
			assert.True(t, fromDoc.fromDocument)
			fromDoc.fromDocument = false
			assert.Equal(t, fromFile, fromDoc)
		})
	}
}

func TestLoadDocument(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app")

	cfg, err := LoadDocument(path, []byte(`{
		"tasks": {"backup": {"run": "true", "cron": "0 3 * * *", "max_concurrent": 2, "retry_attempts": 3}},
		"route": [{"match": {"failure": true}, "notifiers": ["log"]}],
		"notifiers": {"log": {"type": "webhook", "url": "https://example.com/hook"}}
	}`))
	require.NoError(t, err)
	require.Len(t, cfg.Tasks, 1)
	assert.Equal(t, 2, cfg.Tasks[0].MaxConcurrent)
	assert.Equal(t, 3, cfg.Tasks[0].RetryAttempts)
	assert.Equal(t, []string{"log"}, cfg.Notify.Routes[0].NotifierID)
}

func TestLoadDocumentErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app")
	cases := map[string]struct {
		doc  string
		want string
	}{
		"not an object":  {`[]`, "must be a JSON object"},
		"trailing data":  {`{} {}`, "unexpected data"},
		"null":           {`{"tasks": {"a": {"run": "true", "cron": null}}}`, "tasks.a.cron: null"},
		"float into int": {`{"tasks": {"a": {"run": "true", "max_concurrent": 1.5}}}`, "max_concurrent"},
		"invalid task":   {`{"tasks": {"a": {"cron": "* * * * *"}}}`, "run command is required"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := LoadDocument(path, []byte(tc.doc))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// Line numbers would point into TOML the app never wrote, so an unknown key is
// named by its key path instead.
func TestLoadDocumentUnknownKeyNamesKeyPath(t *testing.T) {
	_, err := LoadDocument(filepath.Join(t.TempDir(), "app"),
		[]byte(`{"tasks": {"a": {"run": "true", "retry_atempts": 3}}}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown key "retry_atempts" at tasks.a.retry_atempts`)
	assert.Contains(t, err.Error(), `did you mean "retry_attempts"?`)
	assert.NotContains(t, err.Error(), "line ")

	var located *LocatedError
	require.True(t, errors.As(err, &located))
	assert.Equal(t, "tasks.a.retry_atempts", located.Key)
	assert.Zero(t, located.Line)
}
