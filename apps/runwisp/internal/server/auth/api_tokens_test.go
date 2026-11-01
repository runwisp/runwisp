// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTokenService(t *testing.T) *Service {
	t.Helper()
	svc, err := NewService("pass", []byte("session-key"), nil)
	require.NoError(t, err)
	return svc
}

func TestAPITokens_OperatorAndViewer(t *testing.T) {
	svc := newTokenService(t)
	svc.SetAPITokens([]string{"op-token", "op-token-2"}, []string{"view-token"})

	readOnly, ok := svc.VerifyToken("op-token")
	assert.True(t, ok)
	assert.False(t, readOnly)
	readOnly, ok = svc.VerifyToken("op-token-2")
	assert.True(t, ok)
	assert.False(t, readOnly)

	readOnly, ok = svc.VerifyToken("view-token")
	assert.True(t, ok)
	assert.True(t, readOnly)

	for _, bad := range []string{"", "unknown", "op-token ", "op-toke", "view-token-2"} {
		_, ok = svc.VerifyToken(bad)
		assert.False(t, ok, "%q", bad)
	}
}

func TestAPITokens_SessionTokensStillWork(t *testing.T) {
	svc := newTokenService(t)
	svc.SetAPITokens([]string{"op-token"}, nil)
	readOnly, ok := svc.VerifyToken(svc.IssueToken(SessionDuration))
	assert.True(t, ok)
	assert.False(t, readOnly)
}

func TestAPITokens_SetReplacesAndEmptyDisables(t *testing.T) {
	svc := newTokenService(t)
	svc.SetAPITokens([]string{"old-op"}, []string{"old-view"})
	svc.SetAPITokens([]string{"new-op"}, []string{"new-view"})

	for _, gone := range []string{"old-op", "old-view"} {
		_, ok := svc.VerifyToken(gone)
		assert.False(t, ok, gone)
	}
	_, ok := svc.VerifyToken("new-op")
	assert.True(t, ok)
	readOnly, ok := svc.VerifyToken("new-view")
	assert.True(t, ok)
	assert.True(t, readOnly)

	svc.SetAPITokens(nil, nil)
	for _, gone := range []string{"new-op", "new-view"} {
		_, ok := svc.VerifyToken(gone)
		assert.False(t, ok, gone)
	}
}

func TestAPITokens_UnsetByDefault(t *testing.T) {
	_, ok := newTokenService(t).VerifyToken("anything")
	assert.False(t, ok)
}
