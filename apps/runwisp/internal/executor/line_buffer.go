// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package executor

// LineBuffer accumulates raw bytes and emits complete lines (terminated by '\n')
// via a callback. Partial lines are buffered until a newline arrives or the
// buffer exceeds MaxLineBufferSize, in which case it is flushed as-is.
type LineBuffer struct {
	buf     []byte
	onLine  func(line string)
	maxSize int
}

func NewLineBuffer(onLine func(line string)) *LineBuffer {
	return &LineBuffer{
		buf:     make([]byte, 0, InitialLineBufferSize),
		onLine:  onLine,
		maxSize: MaxLineBufferSize,
	}
}

func (lb *LineBuffer) Write(chunk []byte) {
	start := 0
	for i, b := range chunk {
		if b == '\n' {
			lb.buf = append(lb.buf, chunk[start:i+1]...)
			lb.onLine(string(lb.buf))
			lb.buf = lb.buf[:0]
			start = i + 1
		}
	}
	if start < len(chunk) {
		lb.buf = append(lb.buf, chunk[start:]...)
		if len(lb.buf) > lb.maxSize {
			lb.onLine(string(lb.buf))
			lb.buf = lb.buf[:0]
		}
	}
}

func (lb *LineBuffer) Flush() {
	if len(lb.buf) > 0 {
		lb.onLine(string(lb.buf) + "\n")
		lb.buf = lb.buf[:0]
	}
}
