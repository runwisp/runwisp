// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/runwisp/runwisp/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- mapDomainError ---

func TestMapDomainError_NilReturnsNil(t *testing.T) {
	result := mapDomainError(context.Background(), nil, "fallback")
	assert.Nil(t, result)
}

func TestMapDomainError_ErrTaskNotFound_Returns404(t *testing.T) {
	result := mapDomainError(context.Background(), ErrTaskNotFound, "fallback")
	require.NotNil(t, result)
	assert.Equal(t, http.StatusNotFound, result.GetStatus())
}

func TestMapDomainError_ErrRunNotFound_Returns404(t *testing.T) {
	result := mapDomainError(context.Background(), ErrRunNotFound, "fallback")
	require.NotNil(t, result)
	assert.Equal(t, http.StatusNotFound, result.GetStatus())
}

func TestMapDomainError_StorageErrNotFound_Returns404(t *testing.T) {
	result := mapDomainError(context.Background(), storage.ErrNotFound, "fallback")
	require.NotNil(t, result)
	assert.Equal(t, http.StatusNotFound, result.GetStatus())
}

func TestMapDomainError_WrappedErrTaskNotFound_Returns404(t *testing.T) {
	wrapped := fmt.Errorf("context: %w", ErrTaskNotFound)
	result := mapDomainError(context.Background(), wrapped, "fallback")
	require.NotNil(t, result)
	assert.Equal(t, http.StatusNotFound, result.GetStatus())
}

func TestMapDomainError_ErrManualTriggerDisabled_Returns403(t *testing.T) {
	result := mapDomainError(context.Background(), ErrManualTriggerDisabled, "fallback")
	require.NotNil(t, result)
	assert.Equal(t, http.StatusForbidden, result.GetStatus())
}

func TestMapDomainError_ErrServiceNotRunnable_Returns409(t *testing.T) {
	result := mapDomainError(context.Background(), ErrServiceNotRunnable, "fallback")
	require.NotNil(t, result)
	assert.Equal(t, http.StatusConflict, result.GetStatus())
}

func TestMapDomainError_ErrCannotDeleteActiveRun_Returns409(t *testing.T) {
	result := mapDomainError(context.Background(), ErrCannotDeleteActiveRun, "fallback")
	require.NotNil(t, result)
	assert.Equal(t, http.StatusConflict, result.GetStatus())
}

func TestMapDomainError_ErrNotAService_Returns400(t *testing.T) {
	result := mapDomainError(context.Background(), ErrNotAService, "fallback")
	require.NotNil(t, result)
	assert.Equal(t, http.StatusBadRequest, result.GetStatus())
}

func TestMapDomainError_ErrNotRunning_Returns400(t *testing.T) {
	result := mapDomainError(context.Background(), ErrNotRunning, "fallback")
	require.NotNil(t, result)
	assert.Equal(t, http.StatusBadRequest, result.GetStatus())
}

func TestMapDomainError_UnknownError_Returns500WithFallbackMessage(t *testing.T) {
	result := mapDomainError(context.Background(), errors.New("some unexpected failure"), "Failed to trigger run")
	require.NotNil(t, result)
	assert.Equal(t, http.StatusInternalServerError, result.GetStatus())
	assert.Equal(t, "Failed to trigger run", result.Error())
}

func TestMapDomainError_EmptyFallback_Returns500WithDefaultMessage(t *testing.T) {
	result := mapDomainError(context.Background(), errors.New("boom"), "")
	require.NotNil(t, result)
	assert.Equal(t, http.StatusInternalServerError, result.GetStatus())
	assert.Equal(t, "Internal server error", result.Error())
}

// A client that disconnects mid-query interrupts the in-flight SQLite read,
// surfacing as a driver error the switch would otherwise collapse into a 500.
// Because the request context is already cancelled, that's a client
// cancellation — map it to 408, not a manufactured server fault.
func TestMapDomainError_CancelledContext_Returns408(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// "interrupted (9)" is the modernc SQLite interrupt error string — an
	// opaque driver error that is not errors.Is(context.Canceled); the ctx
	// check is what catches it.
	result := mapDomainError(ctx, errors.New("interrupted (9)"), "Failed to get runs")
	require.NotNil(t, result)
	assert.Equal(t, http.StatusRequestTimeout, result.GetStatus())
}

func TestRespondError_SetsStatusAndBody(t *testing.T) {
	w := httptest.NewRecorder()
	respondError(w, http.StatusBadRequest, "bad input", nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.True(t, strings.Contains(w.Body.String(), "bad input"))
}
