// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package station

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestLowerThreadPriority_NicesOnlyItsThread(t *testing.T) {
	// getpriority(2) answers 20 - nice.
	before, err := unix.Getpriority(unix.PRIO_PROCESS, 0)
	require.NoError(t, err)
	niced := make(chan int)
	go func() {
		lowerThreadPriority()
		prio, _ := unix.Getpriority(unix.PRIO_PROCESS, unix.Gettid())
		niced <- prio
	}()
	assert.Equal(t, 20-grepNice, <-niced)
	after, err := unix.Getpriority(unix.PRIO_PROCESS, 0)
	require.NoError(t, err)
	assert.Equal(t, before, after, "the other threads keep theirs")
}
