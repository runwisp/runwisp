// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package cloud

import (
	"testing"

	"github.com/runwisp/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
)

// The wire enums in packages/asyncapi/asyncapi.yaml are hand-mirrored against
// the daemon's model constants — there is no generated linkage between them. A
// rename on one side without the other silently strands runs (a value the peer
// can't map). These tests are that linkage: they fail if the two lists drift.

func enumStrings(t *testing.T, vals []any) []string {
	t.Helper()
	out := make([]string, len(vals))
	for i, v := range vals {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("enum value %d is %#v, not a string", i, v)
		}
		out[i] = s
	}
	return out
}

// TestProtocolEnumsMatchModel pins each wire enum that has a daemon-side twin to
// that twin's constants, so a rename in model.go or asyncapi.yaml alone breaks.
func TestProtocolEnumsMatchModel(t *testing.T) {
	logOnFull := []string{model.LogOverflowDropNew, model.LogOverflowDropOld, model.LogOverflowKill}
	assert.ElementsMatch(t, logOnFull, enumStrings(t, protocol.ExecutionTaskConfigLogOnFullValues))
	assert.ElementsMatch(t, logOnFull, enumStrings(t, protocol.ServiceTaskConfigLogOnFullValues))

	assert.ElementsMatch(t,
		[]string{string(model.BackoffConstant), string(model.BackoffLinear), string(model.BackoffExponential)},
		enumStrings(t, protocol.ServiceRestartBackoffValues))

	assert.ElementsMatch(t,
		[]string{model.ServiceRunning, model.ServiceDegraded, model.ServiceStopped, model.ServiceFatal},
		enumStrings(t, protocol.ServiceStateValues))

	assert.ElementsMatch(t,
		[]string{model.ServiceInstanceRunning, model.ServiceInstanceRestarting, model.ServiceInstanceStopped, model.ServiceInstanceFatal},
		enumStrings(t, protocol.ServiceInstanceStateValues))
}

// TestProtocolStreamEnumsAgree guards the log-line stream enum, which the
// generator emits three times (LogLineEntry rides the replay `lines` array,
// the search `hits` array, and LogLineMessage's allOf composition — each a
// separate generateToFiles() call with no visibility into the others).
// Nothing forces those copies to stay in sync, so assert they do.
func TestProtocolStreamEnumsAgree(t *testing.T) {
	stream := enumStrings(t, protocol.StreamValues)
	assert.ElementsMatch(t, stream, enumStrings(t, protocol.LinesItemStreamValues))
	assert.ElementsMatch(t, stream, enumStrings(t, protocol.HitsItemStreamValues))
	assert.ElementsMatch(t, []string{"stdout", "stderr", "system"}, stream)
}

// TestProtocolWireOnlyEnumsFrozen pins the enums with no daemon-side constant
// (service-control action, execution status), so a silent asyncapi rename fails
// here rather than in the field.
func TestProtocolWireOnlyEnumsFrozen(t *testing.T) {
	assert.ElementsMatch(t, []string{"start", "stop", "restart"}, enumStrings(t, protocol.ActionValues))
	assert.ElementsMatch(t,
		[]string{"running", "succeeded", "failed", "stopped", "timeout", "skipped"},
		enumStrings(t, protocol.ExecutionStatusValues))
}
