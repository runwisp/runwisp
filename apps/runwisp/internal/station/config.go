// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package station

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"log/slog"
)

const (
	defaultStationURL   = "https://app.runwisp.com"
	requestTimeout      = 30 * time.Second
	maxProtocolLogLines = 5000
)

type Config struct {
	Enabled      bool
	BaseURL      *url.URL
	StationToken string
	AgentVersion string
	Fingerprint  string
}

// LoadConfig loads station configuration. CLI overrides (tokenOverride, urlOverride)
// take precedence over environment variables. fingerprint must be pre-resolved
// by the caller (from persistent storage or environment).
func LoadConfig(agentVersion, tokenOverride, urlOverride, fingerprint string) (Config, error) {
	stationToken := strings.TrimSpace(tokenOverride)
	if stationToken == "" {
		stationToken = strings.TrimSpace(os.Getenv("RUNWISP_STATION_TOKEN"))
	}
	if stationToken == "" {
		return Config{Enabled: false}, nil
	}

	stationURLRaw := strings.TrimSpace(urlOverride)
	if stationURLRaw == "" {
		stationURLRaw = strings.TrimSpace(os.Getenv("RUNWISP_STATION_URL"))
	}
	if stationURLRaw == "" {
		stationURLRaw = defaultStationURL
	}

	baseURL, err := url.Parse(stationURLRaw)
	if err != nil {
		return Config{}, fmt.Errorf("invalid RUNWISP_STATION_URL: %w", err)
	}

	if baseURL.Scheme != "https" && baseURL.Scheme != "http" {
		return Config{}, fmt.Errorf("invalid RUNWISP_STATION_URL scheme %q (expected https or http)", baseURL.Scheme)
	}

	if baseURL.Scheme == "http" {
		if !strings.EqualFold(os.Getenv("RUNWISP_STATION_ALLOW_INSECURE"), "true") {
			return Config{}, fmt.Errorf("insecure http:// station URL rejected; set RUNWISP_STATION_ALLOW_INSECURE=true to allow")
		}
		slog.Warn("RUNWISP_STATION_ALLOW_INSECURE=true: control-plane traffic (bearer token, dispatch frames) runs over plaintext with no TLS — a network attacker can read the token and inject task dispatches; never use this outside local testing",
			"url", baseURL.Redacted())
	}

	if baseURL.Host == "" {
		return Config{}, fmt.Errorf("invalid RUNWISP_STATION_URL: host is required")
	}

	if agentVersion == "" {
		agentVersion = "0.0.0"
	}

	return Config{
		Enabled:      true,
		BaseURL:      baseURL,
		StationToken: stationToken,
		AgentVersion: agentVersion,
		Fingerprint:  fingerprint,
	}, nil
}

func (cfg Config) WebSocketURL() string {
	wsURL := *cfg.BaseURL
	if wsURL.Scheme == "https" {
		wsURL.Scheme = "wss"
	} else {
		wsURL.Scheme = "ws"
	}
	wsURL.Path = "/api/v1/runner/ws"
	wsURL.RawQuery = ""
	wsURL.Fragment = ""
	return wsURL.String()
}

func (cfg Config) TaskSyncURL() string {
	syncURL := *cfg.BaseURL
	syncURL.Path = "/api/v1/runner/tasks/sync"
	syncURL.RawQuery = ""
	syncURL.Fragment = ""
	return syncURL.String()
}
