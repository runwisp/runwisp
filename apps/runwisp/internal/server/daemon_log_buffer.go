// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"strings"
	"sync"
)

// DaemonLogBuffer is a thread-safe ring buffer that stores recent daemon log
// lines and supports live subscriptions for SSE streaming.
type DaemonLogBuffer struct {
	mu          sync.Mutex
	lines       []string
	head        int // next write position
	count       int
	capacity    int
	subscribers map[int]chan string
	nextSubID   int
}

// NewDaemonLogBuffer creates a ring buffer that stores up to capacity log lines.
func NewDaemonLogBuffer(capacity int) *DaemonLogBuffer {
	return &DaemonLogBuffer{
		lines:       make([]string, capacity),
		capacity:    capacity,
		subscribers: make(map[int]chan string),
	}
}

// Write implements io.Writer. It splits input on newlines, stores each line,
// and fans out to all active subscribers.
func (b *DaemonLogBuffer) Write(p []byte) (int, error) {
	raw := strings.TrimRight(string(p), "\r\n")
	if raw == "" {
		return len(p), nil
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}

		b.lines[b.head] = line
		b.head = (b.head + 1) % b.capacity
		if b.count < b.capacity {
			b.count++
		}

		for _, ch := range b.subscribers {
			select {
			case ch <- line:
			default:
			}
		}
	}

	return len(p), nil
}

// Lines returns the last n stored lines in chronological order.
func (b *DaemonLogBuffer) Lines(n int) []string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.linesLocked(n)
}

func (b *DaemonLogBuffer) linesLocked(n int) []string {
	if n > b.count {
		n = b.count
	}
	if n == 0 {
		return nil
	}

	result := make([]string, n)
	start := (b.head - n + b.capacity) % b.capacity
	for i := range n {
		result[i] = b.lines[(start+i)%b.capacity]
	}
	return result
}

// Subscribe returns a channel that receives new log lines as they arrive.
// Call Unsubscribe with the returned ID to clean up.
func (b *DaemonLogBuffer) Subscribe() (int, <-chan string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.subscribeLocked()
}

// SubscribeWithBacklog atomically registers a subscriber and snapshots the
// last n lines under a single lock acquisition, so no line written between
// the two can be missed or double-delivered — unlike calling Lines then
// Subscribe separately, which leaves a gap where a concurrent Write reaches
// neither the snapshot nor (yet) a registered subscriber.
func (b *DaemonLogBuffer) SubscribeWithBacklog(n int) (int, []string, <-chan string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	backlog := b.linesLocked(n)
	id, ch := b.subscribeLocked()
	return id, backlog, ch
}

func (b *DaemonLogBuffer) subscribeLocked() (int, <-chan string) {
	id := b.nextSubID
	b.nextSubID++
	ch := make(chan string, 64)
	b.subscribers[id] = ch
	return id, ch
}

// Unsubscribe removes a subscriber and closes its channel.
func (b *DaemonLogBuffer) Unsubscribe(id int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if ch, ok := b.subscribers[id]; ok {
		delete(b.subscribers, id)
		close(ch)
	}
}
