// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/go-chi/jwtauth/v5"
	"github.com/lestrrat-go/jwx/v3/jwt"
	"github.com/runwisp/runwisp/internal/server"
	"github.com/runwisp/runwisp/internal/storage"
	"github.com/runwisp/runwisp/internal/storage/secretcipher"
)

// localJWTTTL bounds how long a CLI-minted JWT is honored by the daemon's
// middleware. Five minutes is enough for a TUI session to bootstrap or a
// one-shot exec, short enough that an accidentally-leaked token from a
// shell history is mostly stale by the time anyone notices.
const localJWTTTL = 5 * time.Minute

// mintLocalJWT reads the daemon's JWT signing secret straight off disk and
// mints an HS256 token. Used by `runwisp`, `runwisp exec`, and `runwisp tui`
// to authenticate against a daemon running on the same data dir without
// re-running the SRP exchange.
//
// Trust model: anyone who can read the data dir can read the JWT signing
// secret directly. Minting tokens is no escalation over what FS access
// already provides. The 5-minute TTL bounds the blast radius if a token is
// accidentally copied somewhere else (shell history, CI artifact).
//
// dbPath is the path to runwisp.db. The cipher is constructed from
// RUNWISP_DATA_KEY if set; passing the wrong key (or none when the db is
// encrypted) produces a clear error and an empty token.
func mintLocalJWT(dbPath string) (string, error) {
	cipher, err := secretcipher.FromEnv()
	if err != nil {
		return "", err
	}
	db, err := storage.New(dbPath, io.Discard, cipher)
	if err != nil {
		return "", fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	secret, found, err := db.GetSecret(storage.ConfigKeyJWTSecret)
	if err != nil {
		return "", fmt.Errorf("read jwt secret: %w", err)
	}
	if !found || secret == "" {
		return "", errors.New("jwt secret is not initialized in the data dir; start the daemon once to seed it")
	}

	jwtAuth := jwtauth.New(
		"HS256", []byte(secret), nil,
		jwt.WithIssuer(server.JWTIssuer),
		jwt.WithAudience(server.JWTAudience),
	)
	_, ts, err := jwtAuth.Encode(map[string]any{
		"exp":   time.Now().Add(localJWTTTL).Unix(),
		"iat":   time.Now().Unix(),
		"iss":   server.JWTIssuer,
		"aud":   server.JWTAudience,
		"local": true,
	})
	if err != nil {
		return "", fmt.Errorf("encode jwt: %w", err)
	}
	return ts, nil
}
