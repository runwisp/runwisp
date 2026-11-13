// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package station

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/runwisp/runwisp/apps/runwisp/internal/generated/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixturesDir holds one full envelope per message type, shared with the
// station so both sides decode the same frames.
const fixturesDir = "../../../../packages/asyncapi/fixtures"

// payloadTypes decodes each message's data; nil means it carries none.
var payloadTypes = map[string]func() any{
	"hello":              func() any { return &protocol.Hello{} },
	"welcome":            func() any { return &protocol.Welcome{} },
	"goodbye":            func() any { return &protocol.Goodbye{} },
	"ping":               nil,
	"pong":               nil,
	"result":             func() any { return &protocol.Result{} },
	"ack":                func() any { return &protocol.Ack{} },
	"session:update":     func() any { return &protocol.SessionUpdate{} },
	"agent:update":       func() any { return &protocol.AgentUpdate{} },
	"agent:stats":        func() any { return &protocol.AgentStats{} },
	"agent:restart":      func() any { return &protocol.Empty{} },
	"agent:upgrade":      func() any { return &protocol.AgentUpgrade{} },
	"agent:credentials":  func() any { return &protocol.AgentCredentials{} },
	"tasks:sync":         func() any { return &protocol.TasksSync{} },
	"execution:dispatch": func() any { return &protocol.ExecutionDispatch{} },
	"execution:stop":     func() any { return &protocol.ExecutionStop{} },
	"execution:update":   func() any { return &protocol.ExecutionUpdate{} },
	"log:subscribe":      func() any { return &protocol.LogSubscribe{} },
	"log:unsubscribe":    func() any { return &protocol.LogUnsubscribe{} },
	"log:lines":          func() any { return &protocol.LogLines{} },
	"log:search":         func() any { return &protocol.LogSearch{} },
	"upload:request":     func() any { return &protocol.UploadRequest{} },
	"service:apply":      func() any { return &protocol.ServiceApply{} },
	"service:control":    func() any { return &protocol.ServiceControl{} },
	"service:remove":     func() any { return &protocol.ServiceRemove{} },
	"service:status":     func() any { return &protocol.ServiceStatus{} },
}

// TestFixturesCoverTheSpec: every message the spec names has a fixture and a
// payload type here, and nothing else does.
func TestFixturesCoverTheSpec(t *testing.T) {
	spec, err := os.ReadFile(filepath.Join(fixturesDir, "..", "asyncapi.yaml"))
	require.NoError(t, err)
	var names []string
	for _, m := range regexp.MustCompile(`(?m)^      name: ([a-z:]+)$`).FindAllSubmatch(spec, -1) {
		names = append(names, string(m[1]))
	}
	files, err := filepath.Glob(filepath.Join(fixturesDir, "*.json"))
	require.NoError(t, err)
	var fixtures []string
	for _, f := range files {
		fixtures = append(fixtures, strings.ReplaceAll(strings.TrimSuffix(filepath.Base(f), ".json"), "_", ":"))
	}
	var typed []string
	for name := range payloadTypes {
		typed = append(typed, name)
	}
	assert.ElementsMatch(t, names, fixtures)
	assert.ElementsMatch(t, names, typed)
}

// TestFixturesRoundTrip decodes each fixture into the generated types and
// encodes it back unchanged: a renamed field or a lost value breaks it.
func TestFixturesRoundTrip(t *testing.T) {
	for typ, newPayload := range payloadTypes {
		t.Run(typ, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(fixturesDir, strings.ReplaceAll(typ, ":", "_")+".json"))
			require.NoError(t, err)

			var env protocol.Envelope
			require.NoError(t, json.Unmarshal(raw, &env))
			assert.Equal(t, typ, env.Type)
			again, err := json.Marshal(env)
			require.NoError(t, err)
			assert.JSONEq(t, string(raw), string(again))

			if newPayload == nil {
				assert.Empty(t, env.Data)
				return
			}
			dec := json.NewDecoder(strings.NewReader(string(env.Data)))
			dec.DisallowUnknownFields()
			payload := newPayload()
			require.NoError(t, dec.Decode(payload), "every fixture field exists in the generated type")
			data, err := json.Marshal(payload)
			require.NoError(t, err)
			assert.JSONEq(t, string(env.Data), string(data))
		})
	}
}
