// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// tlsHandshakeByte is the first byte of every TLS record carrying a handshake.
const tlsHandshakeByte = 0x16

// sniffListener wraps the TLS port so a plain-HTTP client gets a redirect to
// https:// instead of Go's "Client sent an HTTP request to an HTTPS server."
// 400. It peeks the first byte of each conn: TLS handshakes are handed to
// Accept, anything else is answered by the redirect handler and closed. The
// peek runs per conn in its own goroutine so a silent client can't stall the
// accept loop.
type sniffListener struct {
	net.Listener
	conns     chan net.Conn
	errs      chan error
	closed    chan struct{}
	closeOnce sync.Once
	redirect  *http.Server
}

func newSniffListener(ln net.Listener) *sniffListener {
	l := &sniffListener{
		Listener: ln,
		conns:    make(chan net.Conn),
		errs:     make(chan error),
		closed:   make(chan struct{}),
		redirect: &http.Server{
			Handler:           http.HandlerFunc(redirectToHTTPS),
			ReadHeaderTimeout: 10 * time.Second,
		},
	}
	go l.acceptLoop()
	return l
}

func (l *sniffListener) acceptLoop() {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			select {
			case l.errs <- err:
			case <-l.closed:
				return
			}
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		go l.sniff(c)
	}
}

func (l *sniffListener) sniff(c net.Conn) {
	br := bufio.NewReader(c)
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	first, err := br.Peek(1)
	_ = c.SetReadDeadline(time.Time{})
	if err != nil {
		_ = c.Close()
		return
	}
	if first[0] != tlsHandshakeByte {
		// Serve returns once the one-shot listener's second Accept fails; the
		// conn itself keeps being served until the redirect is written.
		go func() { _ = l.redirect.Serve(&oneShotListener{conn: &bufferedConn{Conn: c, r: br}}) }()
		return
	}
	select {
	case l.conns <- &bufferedConn{Conn: c, r: br}:
	case <-l.closed:
		_ = c.Close()
	}
}

func (l *sniffListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case err := <-l.errs:
		return nil, err
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *sniffListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return l.Listener.Close()
}

// bufferedConn replays the sniffed byte by reading through the bufio.Reader.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// oneShotListener yields a single conn, then blocks until closed so the
// http.Server serving it doesn't spin.
type oneShotListener struct {
	conn net.Conn
	once sync.Once
}

func (l *oneShotListener) Accept() (net.Conn, error) {
	var c net.Conn
	l.once.Do(func() { c = l.conn })
	if c != nil {
		return c, nil
	}
	return nil, net.ErrClosed
}

func (l *oneShotListener) Close() error   { return nil }
func (l *oneShotListener) Addr() net.Addr { return l.conn.LocalAddr() }

// redirectToHTTPS answers a plain-HTTP request on the TLS port with a
// method-preserving redirect to the same host:port over https. The target is
// the Host the client itself asked for, so a browser is only ever sent back
// to the address it typed; hosts that could rewrite the URL's authority or
// path are refused.
func redirectToHTTPS(w http.ResponseWriter, r *http.Request) {
	if r.Host == "" || strings.ContainsAny(r.Host, `/\@?#%`) {
		http.Error(w, "Client sent an HTTP request to an HTTPS server.", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "https://"+r.Host+r.URL.RequestURI(), http.StatusPermanentRedirect) // NOSONAR: Host is validated above and is the address the client connected to
}
