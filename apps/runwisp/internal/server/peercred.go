// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"net"
	"net/http"

	"log/slog"
)

// localTrustedKey tags conns accepted on the daemon's Unix listener after
// PEERCRED has verified the peer UID matches the daemon's own UID. Handlers
// reach it via IsLocalTrusted.
type localTrustedKey struct{}

// IsLocalTrusted reports whether r was delivered on the Unix socket listener
// by a peer matching the daemon's UID. It is the basis for the
// authOrLocalTrusted middleware that skips JWT verification on the socket
// path; loopback TCP requests still go through CHAP/JWT.
func IsLocalTrusted(r *http.Request) bool {
	return IsLocalTrustedCtx(r.Context())
}

// IsLocalTrustedCtx is the context-only variant of IsLocalTrusted, intended
// for huma handlers that receive a context.Context but no *http.Request.
// Nothing outside this package can construct the localTrustedKey, so the flag
// cannot be forged from another package's context value.
func IsLocalTrustedCtx(ctx context.Context) bool {
	v, _ := ctx.Value(localTrustedKey{}).(bool)
	return v
}

// isLocalCtx is the context-only equivalent of isLocalRequest: true when the
// request arrived on the Unix socket (PEERCRED-verified) or from a TCP
// loopback peer. The peer address comes from the value savePeerAddr stored
// before RealIP could overwrite it, so a spoofed X-Forwarded-For cannot forge
// loopback. Used to gate GET /api/instance, whose payload (datadir, config and
// socket paths) must never reach a non-loopback caller.
func isLocalCtx(ctx context.Context) bool {
	if IsLocalTrustedCtx(ctx) {
		return true
	}
	addr, _ := ctx.Value(peerAddrContextKey).(string)
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// peercredListener wraps a Unix-socket listener and rejects any accepted conn
// whose peer UID is not selfUID. It exists so the second http.Server (which
// is the one tagging requests as local-trusted via ConnContext) only ever
// runs for verified-peer conns; mismatches are closed before ConnContext
// fires, so a misconfigured socket-file mode cannot leak the bypass.
type peercredListener struct {
	net.Listener
	selfUID uint32
}

func (l *peercredListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		uc, ok := c.(*net.UnixConn)
		if !ok {
			slog.Warn("non-unix conn on unix listener; closing", "addr", c.RemoteAddr())
			_ = c.Close()
			continue
		}
		uid, err := peerUID(uc)
		if err != nil {
			slog.Warn("PEERCRED lookup failed; closing socket conn", "err", err)
			_ = c.Close()
			continue
		}
		if uid != l.selfUID {
			slog.Warn("rejected unix socket conn from foreign uid", "peer_uid", uid, "self_uid", l.selfUID)
			_ = c.Close()
			continue
		}
		return c, nil
	}
}
