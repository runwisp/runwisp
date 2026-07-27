// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunOpenAPI_EmitsValidJSONSpec(t *testing.T) {
	old := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	var buf bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(&buf, r)
	}()

	err = runOpenAPI()
	require.NoError(t, w.Close())
	wg.Wait()
	os.Stdout = old
	require.NoError(t, err)

	var doc map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &doc))
	assert.Contains(t, doc, "openapi")
	assert.Contains(t, doc, "paths")
}

// TestOpenAPISpecMatchesCommitted is the drift guard: the spec generated from
// the live huma routes must equal the committed openapi.json, so a route change
// that skips `bun run generate` fails CI instead of shipping a stale contract
// agents rely on. info.version is normalized because the committed file is
// generated with a fixed `-ldflags` version the test binary doesn't carry.
func TestOpenAPISpecMatchesCommitted(t *testing.T) {
	generated, err := openAPISpecJSON()
	require.NoError(t, err)

	committed, err := os.ReadFile("../../openapi.json")
	require.NoError(t, err, "read committed openapi.json")

	var got, want map[string]any
	require.NoError(t, json.Unmarshal(generated, &got))
	require.NoError(t, json.Unmarshal(committed, &want))

	normalizeInfoVersion(got)
	normalizeInfoVersion(want)

	assert.Equal(t, want, got,
		"openapi.json is stale — run `bun run generate` (or `bunx moon run runwisp:openapi`) and commit the result")
}

// normalizeInfoVersion blanks info.version so the comparison ignores the
// build-time version stamp (dev vs 0.0.0-dev) and only guards the API surface.
func normalizeInfoVersion(doc map[string]any) {
	if info, ok := doc["info"].(map[string]any); ok {
		info["version"] = ""
	}
}
