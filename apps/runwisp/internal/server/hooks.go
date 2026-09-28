// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"
	"github.com/runwisp/runwisp/internal/model"
)

// hookMaxFailures rejected token attempts per client IP within
// hookFailureWindow lock that IP out of the hooks until the window slides.
// Only 401s count, so a CI job firing a valid token never gets throttled;
// the cap exists to shut down scanners and runaway misconfigured callers.
const (
	hookMaxFailures   = 20
	hookFailureWindow = time.Minute
)

// hookFailureLimiter answers 429 to a client IP that has burned through
// hookMaxFailures bad tokens in the sliding window, and records every 401 the
// wrapped handler returns. A locked-out IP is refused even with a valid token,
// otherwise the lockout wouldn't stop guessing.
func hookFailureLimiter() func(http.Handler) http.Handler {
	rl := httprate.NewRateLimiter(hookMaxFailures, hookFailureWindow)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := httprate.CanonicalizeIP(hostFromAddr(r.RemoteAddr))
			if _, rate, err := rl.Status(key); err == nil && rate >= hookMaxFailures {
				w.Header().Set("Retry-After", strconv.Itoa(int(hookFailureWindow.Seconds())))
				http.Error(w, "Too many failed trigger token attempts", http.StatusTooManyRequests)
				return
			}
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			if ww.Status() == http.StatusUnauthorized {
				// OnLimit is the counter's only public increment; the response is
				// already written, so the headers it sets are moot.
				rl.OnLimit(ww, r, key)
			}
		})
	}
}

// registerHookRoutes wires the token-authenticated trigger endpoint onto r, a
// chi sub-router outside the session (JWT/CSRF) group, sharing the main
// OpenAPI document — the pattern of registerRateLimitedAuthRoutes.
func (srv *Server) registerHookRoutes(r chi.Router) {
	cfg := huma.DefaultConfig("", "")
	cfg.OpenAPI = srv.api.OpenAPI()
	hookAPI := humachi.New(r, cfg)

	huma.Register(hookAPI, huma.Operation{
		OperationID:   "triggerTaskWithToken",
		Method:        http.MethodPost,
		Path:          "/api/hooks/{taskName}",
		Summary:       "Trigger a run with a per-task trigger token",
		Description:   "For CI and webhooks: authenticates with `Authorization: Bearer <token>` (or, less safely, `?token=`), where the token is one of the task's `trigger_tokens` in runwisp.toml, instead of a session. Enforced even with RUNWISP_AUTH=off. An unknown task, a task without tokens, and a wrong token all return the same 401. After 20 rejected attempts in a minute, the client IP gets 429 until the window slides.",
		Tags:          []string{"Runs"},
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusUnauthorized, http.StatusTooManyRequests},
	}, srv.humaHookTrigger)
}

func (srv *Server) humaHookTrigger(ctx context.Context, input *HookTriggerInput) (*RunOutput, error) {
	task, ok := srv.tasks.Get(input.TaskName)
	if !ok || !tokenMatches(presentedToken(input.Authorization, input.Token), task.TriggerTokens) {
		return nil, huma.ErrorWithHeaders(
			huma.Error401Unauthorized("Invalid or missing trigger token"),
			http.Header{"WWW-Authenticate": {"Bearer"}},
		)
	}
	var params map[string]*string
	if input.Body != nil {
		params = input.Body.Params
	}
	return srv.dispatchTrigger(ctx, input.TaskName, params, model.TriggeredByToken, input.Wait, input.WaitTimeout)
}

// presentedToken picks the caller's token: the Authorization header when one
// is sent (a malformed one yields "" rather than falling back to the query),
// else the ?token= query parameter.
func presentedToken(authorization, query string) string {
	if authorization == "" {
		return query
	}
	scheme, token, found := strings.Cut(authorization, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return token
}

// tokenMatches reports whether presented is one of tokens. Both sides are
// hashed first so the constant-time compare doesn't leak length, and every
// token is checked so timing doesn't reveal which one (or whether any) is
// configured.
func tokenMatches(presented string, tokens []string) bool {
	if presented == "" {
		return false
	}
	got := sha256.Sum256([]byte(presented))
	match := 0
	for _, tok := range tokens {
		want := sha256.Sum256([]byte(tok))
		match |= subtle.ConstantTimeCompare(got[:], want[:])
	}
	return match == 1
}
