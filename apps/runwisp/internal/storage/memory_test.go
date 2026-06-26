// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNew_AppliesMemoryPragmas(t *testing.T) {
	db, err := New(":memory:")
	require.NoError(t, err)
	defer db.Close()

	sdb, ok := db.(*SQLiteDatabase)
	require.True(t, ok)

	var cacheSize, softHeapLimit int64
	require.NoError(t, sdb.db.QueryRow("PRAGMA cache_size;").Scan(&cacheSize))
	require.NoError(t, sdb.db.QueryRow("PRAGMA soft_heap_limit;").Scan(&softHeapLimit))

	require.Equal(t, int64(SQLiteCacheSizeKiB), cacheSize)
	require.Equal(t, int64(SQLiteSoftHeapLimitBytes), softHeapLimit)

	// mmap_size reads back empty on :memory: (mmap is N/A there), so assert the
	// statement is valid for the driver rather than its readback value.
	_, err = sdb.db.Exec("PRAGMA mmap_size=0;")
	require.NoError(t, err)
}

func TestShrinkMemory(t *testing.T) {
	db, err := New(":memory:")
	require.NoError(t, err)
	defer db.Close()

	sdb, ok := db.(*SQLiteDatabase)
	require.True(t, ok)

	require.NoError(t, sdb.ShrinkMemory(t.Context()))
}
