// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package station

import (
	"testing"

	"github.com/runwisp/runwisp/apps/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/apps/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
)

// The wire enums in packages/asyncapi/asyncapi.yaml are hand-mirrored against
// the daemon's model constants — there is no generated linkage between them. A
// rename on one side without the other silently strands runs (a value the peer
// can't map). These tests are that linkage: they fail if the two lists drift.

func enumStrings[T ~string](vals []T) []string {
	out := make([]string, len(vals))
	for i, v := range vals {
		out[i] = string(v)
	}
	return out
}

// TestProtocolEnumsMatchModel pins each wire enum that has a daemon-side twin to
// that twin's constants, so a rename in model.go or asyncapi.yaml alone breaks.
func TestProtocolEnumsMatchModel(t *testing.T) {
	assert.ElementsMatch(t,
		[]string{model.LogOverflowDropNew, model.LogOverflowDropOld, model.LogOverflowKill},
		enumStrings(protocol.LogOnFullValues))

	assert.ElementsMatch(t,
		[]string{string(model.BackoffConstant), string(model.BackoffLinear), string(model.BackoffExponential)},
		enumStrings(protocol.RestartBackoffValues))

	assert.ElementsMatch(t,
		[]string{model.ServiceRunning, model.ServiceDegraded, model.ServiceStopped, model.ServiceFatal},
		enumStrings(protocol.ServiceStateValues))

	assert.ElementsMatch(t,
		[]string{model.ServiceInstanceRunning, model.ServiceInstanceRestarting, model.ServiceInstanceStopped, model.ServiceInstanceFatal},
		enumStrings(protocol.ServiceInstanceStateValues))

	assert.ElementsMatch(t,
		[]string{logutil.StreamStdout, logutil.StreamStderr, logutil.StreamSystem},
		enumStrings(protocol.StreamValues))
}

// TestProtocolWireOnlyEnumsFrozen pins the enums with no daemon-side constant
// (service-control action, execution status), so a silent asyncapi rename fails
// here rather than in the field.
func TestProtocolWireOnlyEnumsFrozen(t *testing.T) {
	assert.ElementsMatch(t, []string{"start", "stop", "restart"}, enumStrings(protocol.ActionValues))
	assert.ElementsMatch(t,
		[]string{"running", "succeeded", "failed", "stopped", "timeout", "skipped"},
		enumStrings(protocol.ExecutionStatusValues))
}
