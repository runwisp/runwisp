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

func loadDaemonConfig(store storage.SecretStore, mode daemonMode) (*daemonConfig, error) {
	if err := bumpSchemaVersion(store); err != nil {
		return nil, err
	}

	fp, err := resolveConfigValue(
		store,
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

	password, _, pwErr := datadir.ResolvePassword(store)
	if pwErr != nil && !cloudCfg.Enabled {
		return nil, pwErr
	}
	// "Generated" here means the daemon owns the password (auto-generated and
	// stored in SQLite). It is safe — and expected — to disclose to the
	// operator on startup. An operator-supplied RUNWISP_PASSWORD is never
	// disclosed.
	passwordGenerated := os.Getenv("RUNWISP_PASSWORD") == "" && password != ""

	jwtSecret, err := resolveJWTSecret(store, os.Getenv("RUNWISP_PASSWORD"))
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

// bumpSchemaVersion records storage.CurrentSchemaVersion in the DB the first
// time a binary of this version boots against the data dir. A version bump
// also rotates the JWT secret (handled below) so any web-UI sessions issued
// by the previous binary are invalidated cleanly.
func bumpSchemaVersion(store storage.SecretStore) error {
	prev, _, err := store.GetConfigValue(storage.ConfigKeySchemaVersion)
	if err != nil {
		return err
	}
	if prev == storage.CurrentSchemaVersion {
		return nil
	}
	if err := store.SetConfigValue(storage.ConfigKeySchemaVersion, storage.CurrentSchemaVersion); err != nil {
		return err
	}
	// Drop the JWT secret. resolveJWTSecret will regenerate one. We can't
	// just call SetConfigValue("") because GetConfigValue treats "" as
	// present-with-empty-value; deleting is the only unambiguous signal.
	if err := dropJWTSecret(store); err != nil {
		return err
	}
	if prev != "" {
		slog.Info("Schema version bumped — rotating JWT secret to invalidate existing sessions",
			"from", prev, "to", storage.CurrentSchemaVersion)
	}
	return nil
}

// dropJWTSecret deletes the stored JWT secret row so resolveJWTSecret will
// generate a fresh one on the same boot. Kept on SecretStore-cast types via
// the underlying ConfigRepository interface so we don't need a public Delete
// method just for this single use.
func dropJWTSecret(store storage.SecretStore) error {
	if deleter, ok := store.(interface{ DeleteConfigValue(string) error }); ok {
		return deleter.DeleteConfigValue(storage.ConfigKeyJWTSecret)
	}
	// Fall back to writing an empty marker that resolveJWTSecret treats as
	// missing. Production *SQLiteDatabase implements the deleter above.
	return store.SetConfigValue(storage.ConfigKeyJWTSecret, "")
}

// resolveConfigValue resolves a config value with the following priority:
//  1. Environment variable override (not persisted to DB)
//  2. Database (canonical store)
//  3. Generated via generate() and persisted to DB
func resolveConfigValue(
	store storage.ConfigRepository,
	dbKey, envKey string,
	generate func() (string, error),
) (string, error) {
	if v := strings.TrimSpace(os.Getenv(envKey)); v != "" {
		return v, nil
	}

	if v, found, err := store.GetConfigValue(dbKey); err != nil {
		return "", err
	} else if found {
		return v, nil
	}

	v, err := generate()
	if err != nil {
		return "", err
	}
	if err := store.SetConfigValue(dbKey, v); err != nil {
		return "", err
	}
	return v, nil
}

// resolveJWTSecret ensures a JWT secret exists in the DB and rotates it when
// the explicit password (RUNWISP_PASSWORD) changes. Auto-generated passwords
// are excluded from tracking so that session tokens survive daemon restarts.
//
// Change detection compares a SHA-256 fingerprint of the env-supplied
// password against the previous boot's fingerprint. The plaintext env
// password is intentionally never persisted — operators set RUNWISP_PASSWORD
// specifically to keep credentials out of the data directory.
func resolveJWTSecret(store storage.SecretStore, envPassword string) (string, error) {
	secret, secretFound, err := store.GetSecret(storage.ConfigKeyJWTSecret)
	if err != nil {
		return "", err
	}
	// Treat empty-but-present (left behind by dropJWTSecret's fallback) as
	// missing so we regenerate.
	if secretFound && secret == "" {
		secretFound = false
	}

	passwordChanged := false
	if envPassword != "" {
		pwHash := hashPassword(envPassword)
		storedHash, hashFound, err := store.GetSecret(storage.ConfigKeyEnvPasswordSum)
		if err != nil {
			return "", err
		}
		passwordChanged = hashFound && storedHash != pwHash
		if !hashFound || passwordChanged {
			if err := store.SetSecret(storage.ConfigKeyEnvPasswordSum, pwHash); err != nil {
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
		if err := store.SetSecret(storage.ConfigKeyJWTSecret, newSecret); err != nil {
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
