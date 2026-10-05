// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"bufio"
	"net"
	"net/http"
	"testing"
	"time"
)

// pipeListener hands out pre-made conns, then blocks until closed.
type pipeListener struct {
	conns  chan net.Conn
	closed chan struct{}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}
func (l *pipeListener) Close() error   { close(l.closed); return nil }
func (l *pipeListener) Addr() net.Addr { return &net.TCPAddr{} }

func newPipeListener() *pipeListener {
	return &pipeListener{conns: make(chan net.Conn, 2), closed: make(chan struct{})}
}

func TestSniffListenerRedirectsPlainHTTP(t *testing.T) {
	pl := newPipeListener()
	l := newSniffListener(pl)
	defer l.Close()

	client, srv := net.Pipe()
	pl.conns <- srv
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))
	go func() { _, _ = client.Write([]byte("GET /runs?x=1 HTTP/1.1\r\nHost: example.test:8080\r\n\r\n")) }()

	resp, err := http.ReadResponse(bufio.NewReader(client), nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusPermanentRedirect {
		t.Fatalf("status = %d, want 308", resp.StatusCode)
	}
	if got, want := resp.Header.Get("Location"), "https://example.test:8080/runs?x=1"; got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}
}

func TestSniffListenerPassesTLSThrough(t *testing.T) {
	pl := newPipeListener()
	l := newSniffListener(pl)
	defer l.Close()

	client, srv := net.Pipe()
	pl.conns <- srv
	go func() { _, _ = client.Write([]byte{tlsHandshakeByte, 3, 1}) }()

	c, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 3)
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if n, err := c.Read(buf); err != nil || n == 0 || buf[0] != tlsHandshakeByte {
		t.Fatalf("Read = %d, %v, buf=%v; want replayed handshake byte", n, err, buf)
	}
}
