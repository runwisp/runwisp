// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package logutil

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRunLogPath(t *testing.T) {
	ts := time.Date(2024, 12, 31, 15, 6, 1, 0, time.UTC)
	got := RunLogPath("/var/data/logs", "hello-world", "01KNQ00CP09CR632BVCF1XY6ZQ", ts)
	assert.Equal(t, filepath.Join("/var/data/logs", "hello-world", "20241231_150601_Y6ZQ.log"), got)
}

func TestRemoveLogFiles(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "test.log")
	for _, suffix := range []string{"", ".idx", ".meta", ".prev", ".idx.prev", ".tidx", ".tidx.prev"} {
		assert.NoError(t, os.WriteFile(base+suffix, []byte("x"), 0644))
	}
	RemoveLogFiles(base)
	for _, suffix := range []string{"", ".idx", ".meta", ".prev", ".idx.prev", ".tidx", ".tidx.prev"} {
		_, err := os.Stat(base + suffix)
		assert.True(t, os.IsNotExist(err), "expected %s to be removed", base+suffix)
	}
}

func TestRemoveEmptyParents(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "task", "sub")
	assert.NoError(t, os.MkdirAll(nested, 0755))
	logFile := filepath.Join(nested, "test.log")

	RemoveEmptyParents(logFile, root)

	_, err := os.Stat(nested)
	assert.True(t, os.IsNotExist(err), "empty nested dir should be removed")
	_, err = os.Stat(filepath.Join(root, "task"))
	assert.True(t, os.IsNotExist(err), "empty task dir should be removed")
	_, err = os.Stat(root)
	assert.NoError(t, err, "root dir should still exist")
}
