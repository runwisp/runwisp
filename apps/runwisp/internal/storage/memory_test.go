// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package storage

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNew_AppliesMemoryPragmas(t *testing.T) {
	db, err := New(":memory:")
	require.NoError(t, err)
	defer db.Close()

	var cacheSize, softHeapLimit int64
	require.NoError(t, db.db.QueryRow("PRAGMA cache_size;").Scan(&cacheSize))
	require.NoError(t, db.db.QueryRow("PRAGMA soft_heap_limit;").Scan(&softHeapLimit))

	require.Equal(t, int64(sqliteCacheSizeKiB), cacheSize)
	require.Equal(t, int64(sqliteSoftHeapLimitBytes), softHeapLimit)

	// mmap_size reads back empty on :memory: (mmap is N/A there), so assert the
	// statement is valid for the driver rather than its readback value.
	_, err = db.db.Exec("PRAGMA mmap_size=0;")
	require.NoError(t, err)
}

func TestShrinkMemory(t *testing.T) {
	db, err := New(":memory:")
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, db.ShrinkMemory(t.Context()))
}
