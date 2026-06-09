// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

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
