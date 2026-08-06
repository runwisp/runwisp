// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	str2duration "github.com/xhit/go-str2duration/v2"

	"github.com/runwisp/runwisp/internal/model"
)

// umaskPattern requires 3 or 4 octal digits. Demanding at least three digits
// rejects the decimal/octal-ambiguous "22" (did the operator mean octal 022 or
// 0026?) — spelling it out as "022" removes all doubt.
var umaskPattern = regexp.MustCompile(`^[0-7]{3,4}$`)

// parseEnvBase resolves the `env_base` key. Empty means "omitted", which
// resolves to model.EnvBaseInherit here rather than being left zero — the
// executor's own check treats the zero value as invalid, so resolving at load
// keeps "not configured" from looking like "configured wrong" at run time.
func parseEnvBase(raw string) (model.EnvBase, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return model.EnvBaseInherit, nil
	}
	base := model.EnvBase(trimmed)
	if !base.Valid() {
		return "", fmt.Errorf("%q must be %q or %q", raw, model.EnvBaseInherit, model.EnvBaseClean)
	}
	return base, nil
}

// parseUmask validates an octal umask string and returns its canonical 4-digit
// form (e.g. "022" → "0022"). Empty means "omitted, inherit the daemon's
// umask". The value must be 3-4 octal digits and no greater than 0777 — the
// leading digit of a 4-digit form is the (umask-irrelevant) special-bits slot
// and must be 0. The result is digit-only, so it is safe to interpolate into
// the child shell wrapper.
func parseUmask(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	if !umaskPattern.MatchString(trimmed) {
		return "", fmt.Errorf("%q must be 3 or 4 octal digits (e.g. \"022\" or \"0027\")", raw)
	}
	v, err := strconv.ParseInt(trimmed, 8, 32)
	if err != nil {
		return "", fmt.Errorf("%q is not a valid octal value", raw)
	}
	if v > 0o777 {
		return "", fmt.Errorf("%q exceeds the maximum umask 0777", raw)
	}
	return fmt.Sprintf("%04o", v), nil
}

// parseDuration parses a human-readable duration accepted by time.ParseDuration
// (e.g. "30m", "2h45m"). An empty string yields zero with no error. Parse
// failures are rewrapped into an operator-readable hint — Go's own
// "time: invalid duration" message never reaches a config error.
func parseDuration(raw string) (time.Duration, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%q is not a valid duration; use a duration like \"30s\", \"5m\", \"2h30m\" (h/m/s)", raw)
	}
	return d, nil
}

// parseKeepFor parses a retention window using the extended syntax that also
// accepts day/week suffixes (e.g. "30d", "2w"). An empty string means
// "omitted, inherit the default". Zero and negative durations are rejected.
func parseKeepFor(raw string) (time.Duration, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, nil
	}
	d, err := str2duration.ParseDuration(trimmed)
	if err != nil {
		return 0, fmt.Errorf("%q is not a valid duration; use a duration like \"30s\", \"5m\", \"2h30m\", \"30d\", \"2w\"", raw)
	}
	if d <= 0 {
		return 0, fmt.Errorf("non-positive duration %q is not allowed; pick a positive duration or omit the field to inherit the default", raw)
	}
	return d, nil
}

// parseLogMaxSize parses a byte size. Empty string means "omitted, inherit
// the default"; zero and negative byte counts are rejected.
func parseLogMaxSize(raw string) (int64, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, nil
	}
	n, err := ParseByteSize(trimmed)
	if err != nil {
		return 0, err
	}
	if n <= 0 {
		return 0, fmt.Errorf("non-positive byte size %q is not allowed; pick a positive byte size or omit the field to inherit the default", raw)
	}
	return n, nil
}

// parseKeepRuns interprets a TOML keep_runs value. An omitted key (nil) inherits
// the [defaults] value via ApplyDefaults; an explicit 0 means "keep no completed
// runs"; any positive integer up to KeepRunsCap is a row-count cap. Negative
// integers are rejected. Above-cap values are rejected by validateKeepRuns.
func parseKeepRuns(raw *int) (*int, error) {
	if raw == nil {
		return nil, nil
	}
	if *raw < 0 {
		return nil, fmt.Errorf("must be a non-negative integer; got %d", *raw)
	}
	return raw, nil
}

func parseScopedByteSize(scope, raw string) (int64, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	n, err := ParseByteSize(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", scope, err)
	}
	return n, nil
}

// parseExternalURL normalises [daemon] external_url: trims surrounding
// whitespace and trailing slashes, and rejects anything that doesn't parse
// as an absolute http(s) URL. Empty is the supported default — no link line
// is rendered in notifications when unset.
func parseExternalURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	trimmed = strings.TrimRight(trimmed, "/")
	u, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid daemon.external_url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("invalid daemon.external_url: must start with http:// or https://")
	}
	if u.Host == "" {
		return "", fmt.Errorf("invalid daemon.external_url: missing host")
	}
	return trimmed, nil
}

// parseTLSMode validates [daemon] tls: empty (apply the "off" default later),
// "auto", or "off". The literal is normalised to lower case so "AUTO"/"Off"
// don't trip validation. The actual auto-vs-off behaviour (loopback stays HTTP,
// non-loopback self-signs) is resolved at boot against the bind host.
func parseTLSMode(raw string) (string, error) {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	switch trimmed {
	case "", TLSModeAuto, TLSModeOff:
		return trimmed, nil
	default:
		return "", fmt.Errorf("invalid daemon.tls: %q (must be \"auto\" or \"off\")", raw)
	}
}

// parseMetricsListen validates [daemon] metrics_listen: empty means "share
// the main UI/REST listener", otherwise it must be a host:port pair the OS
// can bind. Hostname resolution and the actual bind happen at server start —
// we only catch obvious shape errors at config-load time.
func parseMetricsListen(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	_, port, err := net.SplitHostPort(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid daemon.metrics_listen: %w", err)
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 0 || p > 65535 {
		return "", fmt.Errorf("invalid daemon.metrics_listen: %q is not a valid port", port)
	}
	return trimmed, nil
}
