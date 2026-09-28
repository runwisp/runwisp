// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
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
				http.Error(w, "Too many failed hook token attempts", http.StatusTooManyRequests)
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

// registerHookRoutes wires the token-authenticated control routes onto r, a
// chi sub-router outside the session (JWT/CSRF) group, sharing the main
// OpenAPI document — the pattern of registerRateLimitedAuthRoutes.
func (srv *Server) registerHookRoutes(r chi.Router) {
	cfg := huma.DefaultConfig("", "")
	cfg.OpenAPI = srv.api.OpenAPI()
	hookAPI := humachi.New(r, cfg)

	huma.Register(hookAPI, hookOperation(runTaskOperation()), srv.humaHookRun)
	huma.Register(hookAPI, hookOperation(startTaskOperation()), srv.humaHookStart)
	huma.Register(hookAPI, hookOperation(restartTaskOperation()), srv.humaHookRestart)
	huma.Register(hookAPI, hookOperation(stopTaskOperation()), srv.humaHookStop)
}

// hookOperation derives a session operation's hook mirror, and with it the
// naming rule every hook follows: path /api/<x> becomes /api/hooks/<x>, the
// OperationID gains a "hook" prefix (runTask -> hookRunTask), and the
// behavior is the session route's, authenticated by a hook token instead.
func hookOperation(op huma.Operation) huma.Operation {
	op.Path = "/api/hooks/" + strings.TrimPrefix(op.Path, "/api/")
	op.OperationID = "hook" + strings.ToUpper(op.OperationID[:1]) + op.OperationID[1:]
	op.Summary += " (hook token)"
	op.Description = "Same as `" + op.Method + " " + strings.Replace(op.Path, "/api/hooks/", "/api/", 1) + "`, " +
		"but authenticated with one of the unit's `hook_tokens` from runwisp.toml instead of a session: " +
		"`Authorization: Bearer <token>`, or (less safely) `?token=`. manual_trigger does not apply. " +
		"Enforced even with RUNWISP_AUTH=off. An unknown unit, a unit without tokens, and a wrong token all return the same 401; " +
		"a valid token whose `allow` list omits this action gets 403. After 20 rejected tokens in a minute, the client IP gets 429 until the window slides.\n\n" +
		op.Description
	op.Tags = []string{"Hooks"}
	op.Errors = append(op.Errors, http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests)
	return op
}

// authorizeHook resolves the unit a hook request targets and checks its token
// grants action. Every way of not presenting a valid token for an existing
// unit looks the same (401), so the routes can't be used to enumerate names.
func (srv *Server) authorizeHook(name string, auth HookAuthInput, action model.HookAction) (*model.Task, error) {
	task, ok := srv.tasks.Get(name)
	var tokens []model.HookToken
	if ok {
		tokens = task.HookTokens
	}
	hook, matched := matchHookToken(presentedToken(auth.Authorization, auth.Token), tokens)
	if !matched {
		return nil, huma.ErrorWithHeaders(
			huma.Error401Unauthorized("Invalid or missing hook token"),
			http.Header{"WWW-Authenticate": {"Bearer"}},
		)
	}
	if !hook.Allows(action) {
		return nil, huma.Error403Forbidden(fmt.Sprintf("This hook token is not allowed to %s %q", action, name))
	}
	return task, nil
}

func (srv *Server) humaHookRun(ctx context.Context, input *HookRunInput) (*RunOutput, error) {
	task, err := srv.authorizeHook(input.TaskName, input.HookAuthInput, model.HookRun)
	if err != nil {
		return nil, err
	}
	var params map[string]*string
	if input.Body != nil {
		params = input.Body.Params
	}
	run, err := srv.runService.trigger(ctx, task, params, model.TriggeredByHook, input.duration())
	if err != nil {
		return nil, mapDomainError(ctx, err, "Failed to trigger run")
	}
	return &RunOutput{Body: *run}, nil
}

func (srv *Server) humaHookStart(ctx context.Context, input *HookControlInput) (*WaitedRunOutput, error) {
	task, err := srv.authorizeHook(input.TaskName, input.HookAuthInput, model.HookStart)
	if err != nil {
		return nil, err
	}
	run, err := srv.runService.start(ctx, task, model.TriggeredByHook, input.duration())
	if err != nil {
		return nil, mapDomainError(ctx, err, "Failed to start task")
	}
	return waitedRun(run, input.duration()), nil
}

func (srv *Server) humaHookRestart(ctx context.Context, input *HookControlInput) (*WaitedRunOutput, error) {
	task, err := srv.authorizeHook(input.TaskName, input.HookAuthInput, model.HookRestart)
	if err != nil {
		return nil, err
	}
	run, err := srv.runService.restart(ctx, task, model.TriggeredByHook, input.duration())
	if err != nil {
		return nil, mapDomainError(ctx, err, "Failed to restart task")
	}
	return waitedRun(run, input.duration()), nil
}

func (srv *Server) humaHookStop(ctx context.Context, input *HookControlInput) (*struct{}, error) {
	task, err := srv.authorizeHook(input.TaskName, input.HookAuthInput, model.HookStop)
	if err != nil {
		return nil, err
	}
	if err := srv.runService.stop(ctx, task, input.duration()); err != nil {
		return nil, mapDomainError(ctx, err, "Failed to stop task")
	}
	return nil, nil
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

// matchHookToken returns the entry whose token equals presented. Both sides
// are hashed first so the constant-time compare doesn't leak length, and every
// entry is checked without branching on the result so timing doesn't reveal
// which one (or whether any) matched.
func matchHookToken(presented string, tokens []model.HookToken) (model.HookToken, bool) {
	if presented == "" {
		return model.HookToken{}, false
	}
	got := sha256.Sum256([]byte(presented))
	found := -1
	for i, h := range tokens {
		want := sha256.Sum256([]byte(h.Token))
		found = subtle.ConstantTimeSelect(subtle.ConstantTimeCompare(got[:], want[:]), i, found)
	}
	if found < 0 {
		return model.HookToken{}, false
	}
	return tokens[found], true
}
