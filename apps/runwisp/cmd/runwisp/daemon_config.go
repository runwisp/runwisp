// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"

	"log/slog"

	"github.com/runwisp/runwisp/internal/cloud"
	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/datadir"
	"github.com/runwisp/runwisp/internal/fingerprint"
	"github.com/runwisp/runwisp/internal/storage"
	"github.com/runwisp/runwisp/internal/version"
)

// daemonConfig holds resolved configuration and secrets for the daemon.
type daemonConfig struct {
	Fingerprint       string
	CloudConfig       cloud.Config
	Config            *config.Config
	UsingDemo         bool
	Password          string
	PasswordGenerated bool
	JWTSecret         string
}

func loadDaemonConfig(configRepo storage.ConfigRepository, mode daemonMode) (*daemonConfig, error) {
	fp, err := resolveConfigValue(
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
	// Standalone mode: cloudCfg stays zero-value (Enabled: false).

	cfg, usingDemo, err := loadConfigFile(flags.CfgFile, cloudCfg.Enabled)
	if err != nil {
		return nil, err
	}

	if err := config.Validate(cfg); err != nil {
		return nil, err
	}

	password, _, pwErr := datadir.ResolvePassword(flags.DataDir)
	if pwErr != nil && !cloudCfg.Enabled {
		return nil, pwErr
	}
	passwordGenerated := os.Getenv("RUNWISP_PASSWORD") == "" && password != ""

	jwtSecret, err := resolveJWTSecret(configRepo, os.Getenv("RUNWISP_PASSWORD"))
	if err != nil {
		return nil, err
	}

	return &daemonConfig{
		Fingerprint:       fp,
		CloudConfig:       cloudCfg,
		Config:            cfg,
		UsingDemo:         usingDemo,
		Password:          password,
		PasswordGenerated: passwordGenerated,
		JWTSecret:         jwtSecret,
	}, nil
}

// resolveConfigValue resolves a config value with the following priority:
//  1. Environment variable override (not persisted to DB)
//  2. Database (canonical store)
//  3. Generated via generate() and persisted to DB
func resolveConfigValue(
	configRepo storage.ConfigRepository,
	dbKey, envKey string,
	generate func() (string, error),
) (string, error) {
	if v := strings.TrimSpace(os.Getenv(envKey)); v != "" {
		return v, nil
	}

	if v, found, err := configRepo.GetConfigValue(dbKey); err != nil {
		return "", err
	} else if found {
		return v, nil
	}

	v, err := generate()
	if err != nil {
		return "", err
	}
	if err := configRepo.SetConfigValue(dbKey, v); err != nil {
		return "", err
	}
	return v, nil
}

// resolveJWTSecret ensures a JWT secret exists in the DB and rotates it when
// the explicit password (RUNWISP_PASSWORD) changes. Auto-generated passwords
// are excluded from tracking so that session tokens survive daemon restarts.
func resolveJWTSecret(configRepo storage.ConfigRepository, envPassword string) (string, error) {
	secret, secretFound, err := configRepo.GetConfigValue(storage.ConfigKeyJWTSecret)
	if err != nil {
		return "", err
	}

	// Only track password changes when RUNWISP_PASSWORD is explicitly set.
	// Auto-generated passwords change on every restart and must not
	// invalidate existing sessions.
	passwordChanged := false
	if envPassword != "" {
		pwHash := hashPassword(envPassword)
		storedHash, hashFound, err := configRepo.GetConfigValue(storage.ConfigKeyPasswordHash)
		if err != nil {
			return "", err
		}
		passwordChanged = hashFound && storedHash != pwHash
		if !hashFound || passwordChanged {
			if err := configRepo.SetConfigValue(storage.ConfigKeyPasswordHash, pwHash); err != nil {
				return "", err
			}
		}
	}

	if passwordChanged {
		slog.Info("Password changed — rotating JWT secret to invalidate existing sessions")
	}

	if !secretFound || passwordChanged {
		newSecret, genErr := datadir.GenerateJWTSecret()
		if genErr != nil {
			return "", genErr
		}
		if err := configRepo.SetConfigValue(storage.ConfigKeyJWTSecret, newSecret); err != nil {
			return "", err
		}
		secret = newSecret
	}

	return secret, nil
}

func hashPassword(password string) string {
	h := sha256.Sum256([]byte(password))
	return hex.EncodeToString(h[:])
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
