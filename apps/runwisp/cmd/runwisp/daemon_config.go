// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/runwisp/runwisp/internal/chap"
	"github.com/runwisp/runwisp/internal/cloud"
	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/datadir"
	"github.com/runwisp/runwisp/internal/fingerprint"
	"github.com/runwisp/runwisp/internal/storage"
	"github.com/runwisp/runwisp/internal/version"
)

// jwtKDFInfo namespaces the JWT signing-key derivation (folded into the PBKDF2
// salt). Bumping it is the way to force every existing browser session to be
// invalidated on the next restart without changing the operator's
// RUNWISP_PASSWORD.
const jwtKDFInfo = "runwisp-jwt-v1"

// daemonConfig holds resolved configuration and secrets for the daemon.
type daemonConfig struct {
	Fingerprint       string
	CloudConfig       cloud.Config
	Config            *config.Config
	UsingDemo         bool
	Password          string
	PasswordEphemeral bool
	JWTSecret         string
	NoAuth            bool
}

func loadDaemonConfig(ctx context.Context, configRepo storage.ConfigRepository, mode daemonMode, f Flags) (*daemonConfig, error) {
	fp, err := resolveConfigValue(
		ctx,
		configRepo,
		storage.ConfigKeyFingerprint,
		"RUNWISP_FINGERPRINT",
		func() (string, error) { return fingerprint.Generate(), nil },
	)
	if err != nil {
		return nil, err
	}

	var cloudCfg cloud.Config
	if mode == modeCloud {
		// Cloud mode: env vars were already set by cmd_cloud.go, so pass empty
		// overrides and let LoadConfig read from the environment.
		cloudCfg, err = cloud.LoadConfig(version.Version, "", "", fp)
		if err != nil {
			return nil, err
		}
	}

	cfg, usingDemo, err := loadConfigFile(f.CfgFile, cloudCfg.Enabled)
	if err != nil {
		return nil, err
	}

	if err := config.Validate(cfg); err != nil {
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

	jwtSecret, err := deriveJWTSecret(password, fp)
	if err != nil {
		return nil, err
	}

	return &daemonConfig{
		Fingerprint:       fp,
		CloudConfig:       cloudCfg,
		Config:            cfg,
		UsingDemo:         usingDemo,
		Password:          password,
		PasswordEphemeral: ephemeral,
		JWTSecret:         jwtSecret,
		NoAuth:            noAuth,
	}, nil
}

// resolveAuthMode reads RUNWISP_NO_AUTH and decides whether the daemon runs
// with authentication disabled. Only the unambiguous values "1" and "true"
// (case-insensitive) enable it; anything else non-empty is a configuration
// mistake the operator must see, not a value to be guessed at. Combining it
// with RUNWISP_PASSWORD is contradictory — a password that is never checked
// gives a false sense of security — so that is rejected too.
func resolveAuthMode() (noAuth bool, err error) {
	raw := strings.TrimSpace(os.Getenv("RUNWISP_NO_AUTH"))
	if raw == "" {
		return false, nil
	}
	switch strings.ToLower(raw) {
	case "1", "true":
	default:
		return false, fmt.Errorf("RUNWISP_NO_AUTH must be \"1\" or \"true\" when set (got %q)", raw)
	}
	if os.Getenv("RUNWISP_PASSWORD") != "" {
		return false, errors.New("RUNWISP_NO_AUTH and RUNWISP_PASSWORD are mutually exclusive — unset one of them")
	}
	return true, nil
}

// resolvePassword returns the daemon password. If RUNWISP_PASSWORD is set, the
// env value is used and ephemeral=false (sessions stay stable across restarts
// because deriveJWTSecret will produce the same JWT key). Otherwise a fresh
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

// deriveJWTSecret produces the HS256 JWT signing key from the daemon password,
// salted by the per-install fingerprint. Properties:
//
//   - Stable across restarts when both inputs are stable, so a browser
//     session backed by RUNWISP_PASSWORD survives a daemon restart.
//   - Rotates automatically when the password rotates — including the
//     ephemeral-password case, where each boot mints a new password and
//     thus invalidates any prior session.
//   - Different per machine/cwd thanks to the fingerprint salt; the same
//     password on another host does not yield the same signing key.
//
// It uses the SAME deliberately-expensive KDF (PBKDF2-HMAC-SHA256 at
// chap.Iterations) as the CHAP login. This matters because the JWT is
// transmitted in the same channel as the CHAP transcript — cleartext on the
// TLS-less / trusted-LAN deployments the CHAP design explicitly supports. The
// fingerprint salt is built from non-secret inputs (machine-id, cwd, exe,
// hostname), so the signing key's resistance to recovery rests entirely on the
// password's entropy plus the KDF cost. A cheap single-pass KDF here would hand
// an eavesdropper who captured any JWT a fast offline oracle (~one hash per
// guess) for a weak RUNWISP_PASSWORD, silently bypassing the 600k-iteration
// PBKDF2 the CHAP transcript relies on for exactly that threat. Keeping the cost
// at parity closes that shortcut; the one-time ~sub-second derivation at boot is
// negligible.
func deriveJWTSecret(password, fp string) (string, error) {
	salt := []byte(jwtKDFInfo + "\x00" + fp)
	key, err := pbkdf2.Key(sha256.New, password, salt, chap.Iterations, 32)
	if err != nil {
		return "", fmt.Errorf("derive JWT secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(key), nil
}

// resolveConfigValue resolves a config value with the following priority:
//  1. Environment variable override (not persisted to DB)
//  2. Database (canonical store)
//  3. Generated via generate() and persisted to DB
func resolveConfigValue(
	ctx context.Context,
	configRepo storage.ConfigRepository,
	dbKey, envKey string,
	generate func() (string, error),
) (string, error) {
	if v := strings.TrimSpace(os.Getenv(envKey)); v != "" {
		return v, nil
	}

	if v, found, err := configRepo.GetConfigValue(ctx, dbKey); err != nil {
		return "", err
	} else if found {
		return v, nil
	}

	v, err := generate()
	if err != nil {
		return "", err
	}
	if err := configRepo.SetConfigValue(ctx, dbKey, v); err != nil {
		return "", err
	}
	return v, nil
}

func loadConfigFile(path string, cloudEnabled bool) (*config.Config, bool, error) {
	cfg, err := config.Load(path)
	if err == nil {
		return cfg, false, nil
	}

	if !errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}

	if cloudEnabled {
		cfg := &config.Config{}
		config.ApplyDefaults(cfg)
		return cfg, false, nil
	}

	return nil, false, fmt.Errorf("no runwisp.toml found at %s — create one to define your tasks (docs: https://docs.runwisp.com/configuration/overview/)", path)
}
