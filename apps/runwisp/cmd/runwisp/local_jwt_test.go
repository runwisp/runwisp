// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/jwtauth/v5"
	"github.com/lestrrat-go/jwx/v3/jwt"
	"github.com/runwisp/runwisp/internal/datadir"
	"github.com/runwisp/runwisp/internal/server"
	"github.com/runwisp/runwisp/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedDataDir prepares a temp dir with a runwisp.db containing a JWT secret
// matching what mintLocalJWT will read. Returns the dataDir path and the
// secret so the test can also verify tokens against the same secret.
func seedDataDir(t *testing.T) (string, string) {
	t.Helper()
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "runwisp.db")

	db, err := storage.New(dbPath, io.Discard, nil)
	require.NoError(t, err)

	jwtSecret, err := datadir.GenerateJWTSecret()
	require.NoError(t, err)
	require.NoError(t, db.SetSecret(storage.ConfigKeyJWTSecret, jwtSecret))
	require.NoError(t, db.Close())

	return dbPath, jwtSecret
}

func TestMintLocalJWT_TokenIsVerifiableWithStoredSecret(t *testing.T) {
	dbPath, jwtSecret := seedDataDir(t)

	token, err := mintLocalJWT(dbPath)
	require.NoError(t, err)
	require.NotEmpty(t, token)

	// Use the same jwtauth construction the daemon uses to verify the token
	// — this rules out signing-algorithm or claim-shape drift between mint
	// and verify.
	jwtAuth := jwtauth.New(
		"HS256", []byte(jwtSecret), nil,
		jwt.WithIssuer(server.JWTIssuer),
		jwt.WithAudience(server.JWTAudience),
	)
	parsed, err := jwtAuth.Decode(token)
	require.NoError(t, err)

	exp, ok := parsed.Expiration()
	require.True(t, ok)
	assert.True(t, exp.After(time.Now()))
	assert.True(t, exp.Before(time.Now().Add(localJWTTTL+5*time.Second)))
}

func TestMintLocalJWT_TokenCarriesLocalClaim(t *testing.T) {
	dbPath, jwtSecret := seedDataDir(t)

	token, err := mintLocalJWT(dbPath)
	require.NoError(t, err)

	jwtAuth := jwtauth.New(
		"HS256", []byte(jwtSecret), nil,
		jwt.WithIssuer(server.JWTIssuer),
		jwt.WithAudience(server.JWTAudience),
	)
	parsed, err := jwtAuth.Decode(token)
	require.NoError(t, err)

	var local bool
	require.NoError(t, parsed.Get("local", &local))
	assert.True(t, local)
}

func TestMintLocalJWT_MissingSecret(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "runwisp.db")
	// Create an empty DB without seeding the JWT secret.
	db, err := storage.New(dbPath, io.Discard, nil)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	_, err = mintLocalJWT(dbPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "jwt secret is not initialized")
}
