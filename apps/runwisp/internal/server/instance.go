// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"net/http"
	"os"

	"github.com/danielgtaylor/huma/v2"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/version"
)

// InstanceOutput wraps the identity payload returned by GET /api/instance.
type InstanceOutput struct {
	Body model.InstanceInfo
}

// humaGetInstance answers "who are you and where do you live?" for a second
// `runwisp` that found this daemon holding its port. It is local-only: the
// route is public (no JWT/CHAP) so a launcher with no password can reach it,
// but the handler returns 403 to any non-loopback TCP caller so the datadir,
// config and socket paths it discloses never reach the network.
func (srv *Server) humaGetInstance(ctx context.Context, _ *struct{}) (*InstanceOutput, error) {
	if !isLocalCtx(ctx) {
		return nil, huma.Error403Forbidden("instance endpoint is only available locally (Unix socket or loopback)")
	}
	return &InstanceOutput{Body: model.InstanceInfo{
		App:         AppName,
		Version:     version.Version,
		Fingerprint: srv.stats.GetDaemonInfo().Fingerprint,
		Pid:         os.Getpid(),
		DataDir:     srv.dataDir,
		ConfigPath:  srv.configPath,
		SocketPath:  srv.socketPath,
	}}, nil
}

// registerInstanceRoute wires GET /api/instance into the public huma API
// (srv.api), parallel to /health — deliberately outside the authOrLocalTrusted
// group so a password-less launcher can query it. The handler's own loopback
// gate is what keeps it local.
func (srv *Server) registerInstanceRoute() {
	huma.Register(srv.api, huma.Operation{
		OperationID: "getInstance",
		Method:      http.MethodGet,
		Path:        "/api/instance",
		Summary:     "Get local daemon identity (loopback/socket only)",
		Description: "Returns the running daemon's datadir, config path, socket path, pid, version and fingerprint. " +
			"Used by a second `runwisp` that hit a port conflict to discover and offer to connect to or stop this daemon. " +
			"Always 403 over non-loopback TCP — the paths are local-only.",
		Tags: []string{"System"},
	}, srv.humaGetInstance)
}
