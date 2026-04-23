// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"encoding/json"
	"net/http"

	"log/slog"
)

func respondError(resp http.ResponseWriter, status int, publicMsg string, err error) {
	if err != nil && status >= http.StatusInternalServerError {
		slog.Error(publicMsg, "err", err)
	}
	http.Error(resp, publicMsg, status)
}

func respondBadRequest(resp http.ResponseWriter, publicMsg string) {
	respondError(resp, http.StatusBadRequest, publicMsg, nil)
}

func respondNotFound(resp http.ResponseWriter, publicMsg string) {
	respondError(resp, http.StatusNotFound, publicMsg, nil)
}

func respondForbidden(resp http.ResponseWriter, publicMsg string) {
	respondError(resp, http.StatusForbidden, publicMsg, nil)
}

func respondUnauthorized(resp http.ResponseWriter, publicMsg string) {
	respondError(resp, http.StatusUnauthorized, publicMsg, nil)
}

func respondJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("Failed to encode JSON response", "err", err)
	}
}
