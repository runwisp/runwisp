// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/runwisp/runwisp/apps/runwisp/internal/config"
	"github.com/runwisp/runwisp/apps/runwisp/internal/datadir"
	"github.com/runwisp/runwisp/apps/runwisp/internal/fingerprint"
	"github.com/runwisp/runwisp/apps/runwisp/internal/server/auth"
	"github.com/runwisp/runwisp/apps/runwisp/internal/station"
	"github.com/runwisp/runwisp/apps/runwisp/internal/storage"
	"github.com/runwisp/runwisp/apps/runwisp/internal/version"
)

// daemonConfig holds resolved configuration and secrets for the daemon.
type daemonConfig struct {
	Fingerprint       string
	StationConfig     station.Config
	Config            *config.Config
	Password          string
	PasswordEphemeral bool
	SessionKey        []byte
	NoAuth            bool
}

func loadDaemonConfig(ctx context.Context, configRepo *storage.SQLiteDatabase, mode daemonMode, f Flags) (*daemonConfig, error) {
	// Fingerprint resolution priority: an env override (not persisted), then the
	// DB (canonical store), then a freshly generated one persisted for next boot.
	fp, err := resolveFingerprint(ctx, configRepo)
	if err != nil {
		return nil, err
	}

	var stationCfg station.Config
	if mode == modeStation {
		// Station mode: env vars were already set by cmd_station.go, so pass empty
		// overrides and let LoadConfig read from the environment.
		stationCfg, err = station.LoadConfig(version.Version, "", "", fp)
		if err != nil {
			return nil, err
		}
	}

	cfg, err := loadConfigFile(f.CfgFile, stationCfg.Enabled)
	if err != nil {
		return nil, err
	}

	noAuth, err := resolveAuthMode()
	if err != nil {
		return nil, err
	}

	password, ephemeral, err := resolvePassword()
	if err != nil {
		return nil, err
	}

	sessionKey, err := auth.DeriveSessionKey(password, fp)
	if err != nil {
		return nil, err
	}

	return &daemonConfig{
		Fingerprint:       fp,
		StationConfig:     stationCfg,
		Config:            cfg,
		Password:          password,
		PasswordEphemeral: ephemeral,
		SessionKey:        sessionKey,
		NoAuth:            noAuth,
	}, nil
}

// resolveAuthMode reads RUNWISP_AUTH and decides whether the daemon runs with
// authentication disabled. Auth is on by default; only the unambiguous value
// "off" (case-insensitive) turns it off, and "on" restates the default.
// Anything else is a configuration mistake the operator must see, not a value
// to be guessed at. Turning auth off while RUNWISP_PASSWORD is set is
// contradictory — a password that is never checked gives a false sense of
// security — so that is rejected too.
func resolveAuthMode() (noAuth bool, err error) {
	raw := strings.TrimSpace(os.Getenv("RUNWISP_AUTH"))
	if raw == "" {
		return false, nil
	}
	switch strings.ToLower(raw) {
	case "on":
		return false, nil
	case "off":
	default:
		return false, fmt.Errorf("RUNWISP_AUTH must be \"on\" or \"off\" when set (got %q)", raw)
	}
	if os.Getenv("RUNWISP_PASSWORD") != "" {
		return false, errors.New("RUNWISP_AUTH=off and RUNWISP_PASSWORD are mutually exclusive — unset one of them")
	}
	return true, nil
}

// resolvePassword returns the daemon password. If RUNWISP_PASSWORD is set, the
// env value is used and ephemeral=false (sessions stay stable across restarts
// because auth.DeriveSessionKey will produce the same key). Otherwise a fresh
// random password is minted in memory for this boot only; ephemeral=true.
//
// The password is never read from or written to disk. Persisting it would
// undo the whole point of the env-var path (operators set RUNWISP_PASSWORD
// specifically to keep credentials out of the data directory) and would
// expose a durable credential to anyone with a momentary read of the data
// directory.
func resolvePassword() (password string, ephemeral bool, err error) {
	if envPw := os.Getenv("RUNWISP_PASSWORD"); envPw != "" {
		return envPw, false, nil
	}
	pw, err := datadir.GeneratePassword()
	if err != nil {
		return "", false, err
	}
	return pw, true, nil
}

// resolveFingerprint resolves the daemon's per-install fingerprint: an env
// override wins (not persisted), else the DB's stored value, else a freshly
// generated one persisted for next boot.
func resolveFingerprint(ctx context.Context, configRepo *storage.SQLiteDatabase) (string, error) {
	if fp := strings.TrimSpace(os.Getenv("RUNWISP_FINGERPRINT")); fp != "" {
		return fp, nil
	}
	if fp, found, err := configRepo.GetConfigValue(ctx, storage.ConfigKeyFingerprint); err != nil {
		return "", err
	} else if found {
		return fp, nil
	}
	fp := fingerprint.Generate()
	if err := configRepo.SetConfigValue(ctx, storage.ConfigKeyFingerprint, fp); err != nil {
		return "", err
	}
	return fp, nil
}

func loadConfigFile(path string, stationEnabled bool) (*config.Config, error) {
	cfg, err := config.Load(path)
	if err == nil {
		// A root daemon executes whatever the config says; re-assert the file
		// (and its includes) are not reachable through a user-writable path or a
		// repointable symlink before trusting it. No-op when unprivileged.
		if terr := config.AssertPrivilegedConfigTrust(cfg, path); terr != nil {
			return nil, terr
		}
		if perr := config.ApplyTrustedProxiesEnv(cfg); perr != nil {
			return nil, perr
		}
		return cfg, nil
	}

	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	if stationEnabled {
		cfg := &config.Config{}
		config.ApplyDefaults(cfg)
		if perr := config.ApplyTrustedProxiesEnv(cfg); perr != nil {
			return nil, perr
		}
		return cfg, nil
	}

	return nil, noConfigError(path)
}
