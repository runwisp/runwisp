// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package server

import (
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
	result := mapDomainError(nil, "fallback")
	assert.Nil(t, result)
}

func TestMapDomainError_ErrTaskNotFound_Returns404(t *testing.T) {
	result := mapDomainError(ErrTaskNotFound, "fallback")
	require.NotNil(t, result)
	assert.Equal(t, http.StatusNotFound, result.GetStatus())
}

func TestMapDomainError_ErrRunNotFound_Returns404(t *testing.T) {
	result := mapDomainError(ErrRunNotFound, "fallback")
	require.NotNil(t, result)
	assert.Equal(t, http.StatusNotFound, result.GetStatus())
}

func TestMapDomainError_StorageErrNotFound_Returns404(t *testing.T) {
	result := mapDomainError(storage.ErrNotFound, "fallback")
	require.NotNil(t, result)
	assert.Equal(t, http.StatusNotFound, result.GetStatus())
}

func TestMapDomainError_WrappedErrTaskNotFound_Returns404(t *testing.T) {
	wrapped := fmt.Errorf("context: %w", ErrTaskNotFound)
	result := mapDomainError(wrapped, "fallback")
	require.NotNil(t, result)
	assert.Equal(t, http.StatusNotFound, result.GetStatus())
}

func TestMapDomainError_ErrAPIDisabled_Returns403(t *testing.T) {
	result := mapDomainError(ErrAPIDisabled, "fallback")
	require.NotNil(t, result)
	assert.Equal(t, http.StatusForbidden, result.GetStatus())
}

func TestMapDomainError_ErrServiceNotRunnable_Returns409(t *testing.T) {
	result := mapDomainError(ErrServiceNotRunnable, "fallback")
	require.NotNil(t, result)
	assert.Equal(t, http.StatusConflict, result.GetStatus())
}

func TestMapDomainError_ErrCannotDeleteActiveRun_Returns409(t *testing.T) {
	result := mapDomainError(ErrCannotDeleteActiveRun, "fallback")
	require.NotNil(t, result)
	assert.Equal(t, http.StatusConflict, result.GetStatus())
}

func TestMapDomainError_ErrNotAService_Returns400(t *testing.T) {
	result := mapDomainError(ErrNotAService, "fallback")
	require.NotNil(t, result)
	assert.Equal(t, http.StatusBadRequest, result.GetStatus())
}

func TestMapDomainError_ErrNotRunning_Returns400(t *testing.T) {
	result := mapDomainError(ErrNotRunning, "fallback")
	require.NotNil(t, result)
	assert.Equal(t, http.StatusBadRequest, result.GetStatus())
}

func TestMapDomainError_UnknownError_Returns500WithFallbackMessage(t *testing.T) {
	result := mapDomainError(errors.New("some unexpected failure"), "Failed to trigger run")
	require.NotNil(t, result)
	assert.Equal(t, http.StatusInternalServerError, result.GetStatus())
	assert.Equal(t, "Failed to trigger run", result.Error())
}

func TestMapDomainError_EmptyFallback_Returns500WithDefaultMessage(t *testing.T) {
	result := mapDomainError(errors.New("boom"), "")
	require.NotNil(t, result)
	assert.Equal(t, http.StatusInternalServerError, result.GetStatus())
	assert.Equal(t, "Internal server error", result.Error())
}

// --- respondError / respondNotFound ---

func TestRespondError_SetsStatusAndBody(t *testing.T) {
	w := httptest.NewRecorder()
	respondError(w, http.StatusBadRequest, "bad input", nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.True(t, strings.Contains(w.Body.String(), "bad input"))
}

func TestRespondNotFound_Sets404(t *testing.T) {
	w := httptest.NewRecorder()
	respondNotFound(w, "not here")
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.True(t, strings.Contains(w.Body.String(), "not here"))
}
