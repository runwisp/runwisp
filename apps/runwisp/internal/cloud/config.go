// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package cloud

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	defaultCloudURL        = "https://app.runwisp.com"
	defaultRequestTimeout  = 30 * time.Second
	defaultTaskSyncTimeout = 30 * time.Second
	maxProtocolLogLines    = 5000
)

type Config struct {
	Enabled         bool
	BaseURL         *url.URL
	CloudToken      string
	AgentVersion    string
	Fingerprint     string
	RequestTimeout  time.Duration
	TaskSyncTimeout time.Duration
}

// LoadConfig loads cloud configuration. CLI overrides (tokenOverride, urlOverride)
// take precedence over environment variables. fingerprint must be pre-resolved
// by the caller (from persistent storage or environment).
func LoadConfig(agentVersion, tokenOverride, urlOverride, fingerprint string) (Config, error) {
	cloudToken := strings.TrimSpace(tokenOverride)
	if cloudToken == "" {
		cloudToken = strings.TrimSpace(os.Getenv("RUNWISP_CLOUD_TOKEN"))
	}
	if cloudToken == "" {
		return Config{Enabled: false}, nil
	}

	cloudURLRaw := strings.TrimSpace(urlOverride)
	if cloudURLRaw == "" {
		cloudURLRaw = strings.TrimSpace(os.Getenv("RUNWISP_CLOUD_URL"))
	}
	if cloudURLRaw == "" {
		cloudURLRaw = defaultCloudURL
	}

	baseURL, err := url.Parse(cloudURLRaw)
	if err != nil {
		return Config{}, fmt.Errorf("invalid RUNWISP_CLOUD_URL: %w", err)
	}

	if baseURL.Scheme != "https" && baseURL.Scheme != "http" {
		return Config{}, fmt.Errorf("invalid RUNWISP_CLOUD_URL scheme %q (expected https or http)", baseURL.Scheme)
	}

	if baseURL.Scheme == "http" && !strings.EqualFold(os.Getenv("RUNWISP_CLOUD_ALLOW_INSECURE"), "true") {
		return Config{}, fmt.Errorf("insecure http:// cloud URL rejected; set RUNWISP_CLOUD_ALLOW_INSECURE=true to allow")
	}

	if baseURL.Host == "" {
		return Config{}, fmt.Errorf("invalid RUNWISP_CLOUD_URL: host is required")
	}

	if agentVersion == "" {
		agentVersion = "0.0.0"
	}

	return Config{
		Enabled:         true,
		BaseURL:         baseURL,
		CloudToken:      cloudToken,
		AgentVersion:    agentVersion,
		Fingerprint:     fingerprint,
		RequestTimeout:  defaultRequestTimeout,
		TaskSyncTimeout: defaultTaskSyncTimeout,
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
