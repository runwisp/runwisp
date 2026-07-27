// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDaemonLogBuffer_WriteAndLines(t *testing.T) {
	b := NewDaemonLogBuffer(4)
	_, err := b.Write([]byte("first\nsecond\n"))
	require.NoError(t, err)
	_, err = b.Write([]byte("third"))
	require.NoError(t, err)

	all := b.Lines(10)
	assert.Equal(t, []string{"first", "second", "third"}, all)
}

func TestDaemonLogBuffer_WrapsAroundCapacity(t *testing.T) {
	b := NewDaemonLogBuffer(2)
	for _, l := range []string{"a", "b", "c", "d"} {
		_, err := b.Write([]byte(l + "\n"))
		require.NoError(t, err)
	}
	assert.Equal(t, []string{"c", "d"}, b.Lines(10))
}

func TestDaemonLogBuffer_LinesEmpty(t *testing.T) {
	b := NewDaemonLogBuffer(2)
	assert.Nil(t, b.Lines(5))
	assert.Nil(t, b.Lines(0))
}

func TestDaemonLogBuffer_WriteIgnoresEmpty(t *testing.T) {
	b := NewDaemonLogBuffer(2)
	n, err := b.Write([]byte("\n\r\n"))
	require.NoError(t, err)
	assert.Equal(t, 3, n)
	assert.Equal(t, 0, b.count)
}

func TestDaemonLogBuffer_SubscribeReceivesNewLines(t *testing.T) {
	b := NewDaemonLogBuffer(4)
	id, ch := b.Subscribe()

	_, err := b.Write([]byte("hello\nworld\n"))
	require.NoError(t, err)

	assert.Equal(t, "hello", <-ch)
	assert.Equal(t, "world", <-ch)

	b.Unsubscribe(id)
	_, open := <-ch
	assert.False(t, open, "unsubscribe must close the channel")
}

func TestDaemonLogBuffer_UnsubscribeUnknownIDIsNoop(t *testing.T) {
	b := NewDaemonLogBuffer(2)
	b.Unsubscribe(999)
}

func TestDaemonLogBuffer_SubscriberDropOnFull(t *testing.T) {
	b := NewDaemonLogBuffer(4)
	_, ch := b.Subscribe()
	// Don't consume — buffered channel size is 64. Push more than that.
	for i := range 200 {
		_, err := b.Write([]byte("line-" + string(rune('A'+i%26)) + "\n"))
		require.NoError(t, err)
	}
	// We should have received at most 64 lines without panicking.
	count := 0
loop:
	for {
		select {
		case <-ch:
			count++
		default:
			break loop
		}
	}
	assert.LessOrEqual(t, count, 64)
}
