// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package station

import (
	"context"
	"log/slog"
	"os"
	goruntime "runtime"
	"strings"

	"github.com/runwisp/runwisp/apps/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/apps/runwisp/internal/update"
)

// Feature names this daemon advertises in hello. Everything else in the
// protocol is baseline.
const (
	featureAgentRestart = "agent.restart"
	featureAgentUpgrade = "agent.upgrade"
)

// execTypes maps each execution spec type to its feature name.
var execTypes = map[string]string{
	"shell":     "exec.shell",
	"container": "exec.container",
	"compose":   "exec.compose",
	"http":      "exec.http",
	"config":    "exec.config",
}

// featureForExecType names the feature an execution spec type needs; known is
// false for a type this daemon doesn't understand.
func featureForExecType(execType string) (name string, known bool) {
	name, known = execTypes[execType]
	return name, known
}

// features reports every optional feature and whether it can be used now.
func (client *Client) features() map[string]*protocol.FeatureStatus {
	out := make(map[string]*protocol.FeatureStatus, len(execTypes)+2)
	avail := client.handler.liveAvailability()
	for execType, name := range execTypes {
		status := avail.ForType(execType)
		out[name] = &protocol.FeatureStatus{Available: status.Available, Reason: status.Reason}
	}
	out[featureAgentRestart] = featureStatus(client.restartBlocker())
	out[featureAgentUpgrade] = featureStatus(client.upgradeBlocker())
	return out
}

func featureStatus(blocker error) *protocol.FeatureStatus {
	if blocker != nil {
		return &protocol.FeatureStatus{Reason: blocker.Error()}
	}
	return &protocol.FeatureStatus{Available: true}
}

// missingFeatures describes the required features this daemon lacks or can't
// use right now, "" when it has them all.
func (client *Client) missingFeatures(requires []string) string {
	if len(requires) == 0 {
		return ""
	}
	have := client.features()
	var missing []string
	for _, name := range requires {
		switch status, ok := have[name]; {
		case !ok:
			missing = append(missing, name+" (not supported by this daemon)")
		case !status.Available:
			missing = append(missing, name+" ("+status.Reason+")")
		}
	}
	if len(missing) == 0 {
		return ""
	}
	return "missing required features: " + strings.Join(missing, ", ")
}

// agentInfo identifies this daemon in hello and agent:update.
func (client *Client) agentInfo() *protocol.AgentInfo {
	host, _ := os.Hostname()
	info := &protocol.AgentInfo{
		Version: client.config.AgentVersion,
		Os:      goruntime.GOOS,
		Arch:    goruntime.GOARCH,
		Host:    host,
	}
	if client.deps.InstanceName != nil {
		info.Name = client.deps.InstanceName()
	}
	return info
}

var (
	errRestartUnsupported = newError(protocol.ErrorCodeForbidden, "this daemon can't restart itself")
	errUpgradeUnsupported = newError(protocol.ErrorCodeForbidden, "this daemon can't upgrade itself")
)

func (client *Client) restartBlocker() error {
	if client.deps.RequestRestart == nil {
		return errRestartUnsupported
	}
	if client.deps.CanRestart != nil {
		return client.deps.CanRestart()
	}
	return nil
}

func (client *Client) upgradeBlocker() error {
	if client.deps.Upgrade == nil || client.deps.CanUpgrade == nil {
		return errUpgradeUnsupported
	}
	return client.deps.CanUpgrade()
}

// restart answers agent:restart: the result goes out first, then the daemon
// restarts. Refused unless a service manager will bring the daemon back.
func (client *Client) restart() (any, error) {
	if err := client.restartBlocker(); err != nil {
		return nil, newError(protocol.ErrorCodeForbidden, "%s", err.Error())
	}
	slog.Info("restarting on station request")
	return followUp{then: func() {
		if err := client.deps.RequestRestart(); err != nil {
			slog.Error("station-requested restart failed", "err", err)
		}
	}}, nil
}

// upgrade answers agent:upgrade: it installs version from the daemon's own
// release source, answers, then restarts onto it. One upgrade runs at a time.
func (client *Client) upgrade(ctx context.Context, version string) (any, error) {
	if !update.IsRelease(version) {
		return nil, newError(protocol.ErrorCodeInvalid, "version %q is not a release tag", version)
	}
	if err := client.upgradeBlocker(); err != nil {
		return nil, newError(protocol.ErrorCodeForbidden, "%s", err.Error())
	}
	if !client.upgradeMu.TryLock() {
		return nil, newError(protocol.ErrorCodeBusy, "an upgrade is already running")
	}
	defer client.upgradeMu.Unlock()
	tag := "v" + strings.TrimPrefix(strings.TrimSpace(version), "v")
	slog.Info("upgrading on station request", "version", tag)
	restart, err := client.deps.Upgrade(ctx, tag)
	if err != nil {
		return nil, &StationError{Code: protocol.ErrorCodeInternal, Message: "upgrade failed", Retryable: true, Err: err}
	}
	return followUp{then: restart}, nil
}

// rotateToken answers agent:credentials: the new token is stored durably
// before the ok, and the next connection uses it.
func (client *Client) rotateToken(ctx context.Context, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return newError(protocol.ErrorCodeInvalid, "token is required")
	}
	if client.deps.Credentials == nil {
		return newError(protocol.ErrorCodeForbidden, "this daemon can't store a new token")
	}
	if err := saveRotatedToken(ctx, client.deps.Credentials, client.config.ConfiguredToken, token); err != nil {
		return &StationError{Code: protocol.ErrorCodeInternal, Message: "failed to store the new token", Retryable: true, Err: err}
	}
	client.mu.Lock()
	client.token = token
	client.mu.Unlock()
	slog.Info("station rotated the runner token")
	return nil
}
