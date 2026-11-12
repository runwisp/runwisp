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
	defaultStationURL = "https://app.runwisp.com"
	requestTimeout    = 30 * time.Second
)

type Config struct {
	Enabled bool
	BaseURL *url.URL
	// StationToken is the token the daemon dials with: ConfiguredToken, or the
	// one the station rotated in (ApplyRotatedToken).
	StationToken string
	// ConfiguredToken is the token from the flag or RUNWISP_STATION_TOKEN.
	ConfiguredToken string
	AgentVersion    string
	Fingerprint     string
	// AllowInsecure mirrors RUNWISP_STATION_ALLOW_INSECURE=true: besides an
	// http:// station URL it lets log archives go to http:// and private-IP
	// upload URLs. Plain http:// sends the runner token and every command
	// unencrypted: fine on a network you trust (localhost, your LAN, a VPN like
	// Tailscale); use HTTPS anywhere else.
	AllowInsecure bool
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

	allowInsecure := strings.EqualFold(os.Getenv("RUNWISP_STATION_ALLOW_INSECURE"), "true")
	if baseURL.Scheme == "http" {
		if !allowInsecure {
			return Config{}, fmt.Errorf("insecure http:// station URL rejected; set RUNWISP_STATION_ALLOW_INSECURE=true to allow")
		}
		slog.Warn("RUNWISP_STATION_ALLOW_INSECURE=true: Plain http:// sends the runner token and every command unencrypted: fine on a network you trust (localhost, your LAN, a VPN like Tailscale); use HTTPS anywhere else.",
			"url", baseURL.Redacted())
	}

	if baseURL.Host == "" {
		return Config{}, fmt.Errorf("invalid RUNWISP_STATION_URL: host is required")
	}

	if agentVersion == "" {
		agentVersion = "0.0.0"
	}

	return Config{
		Enabled:         true,
		BaseURL:         baseURL,
		StationToken:    stationToken,
		ConfiguredToken: stationToken,
		AgentVersion:    agentVersion,
		Fingerprint:     fingerprint,
		AllowInsecure:   allowInsecure,
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
