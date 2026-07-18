// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package autostart

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRunningUnderServiceManager(t *testing.T) {
	tests := []struct {
		name       string
		managed    string
		invocation string
		want       bool
	}{
		{name: "neither set", managed: "", invocation: "", want: false},
		{name: "managed marker from our templates", managed: "1", invocation: "", want: true},
		{name: "managed marker wrong value", managed: "0", invocation: "", want: false},
		// INVOCATION_ID leaks in from any systemd unit ancestor (login session,
		// terminal, shell), so it must NOT count on its own — a plain
		// `runwisp daemon` carries it but is not service-managed.
		{name: "INVOCATION_ID alone is not enough", managed: "", invocation: "5d10ec423bcf449789f2dfd36760a4ab", want: false},
		{name: "marker set, INVOCATION_ID irrelevant", managed: "1", invocation: "abc", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// t.Setenv with "" pins the var to empty — which the detector
			// treats as unset — and restores any CI-injected value after.
			t.Setenv(ServiceManagedEnv, tt.managed)
			t.Setenv("INVOCATION_ID", tt.invocation)
			assert.Equal(t, tt.want, RunningUnderServiceManager())
		})
	}
}

func TestWithoutServiceEnv(t *testing.T) {
	in := []string{
		"PATH=/usr/bin",
		// INVOCATION_ID is no longer a service marker, so it is left untouched.
		"INVOCATION_ID=5d10ec423bcf449789f2dfd36760a4ab",
		"HOME=/home/op",
		"RUNWISP_SERVICE_MANAGED=1",
		"RUNWISP_DEMO_TEMP=/tmp/runwisp-demo-x",
	}
	got := WithoutServiceEnv(in)
	assert.Equal(t, []string{
		"PATH=/usr/bin",
		"INVOCATION_ID=5d10ec423bcf449789f2dfd36760a4ab",
		"HOME=/home/op",
		"RUNWISP_DEMO_TEMP=/tmp/runwisp-demo-x",
	}, got)
}

func TestWithoutServiceEnv_KeepsLookalikePrefixes(t *testing.T) {
	// A var that merely begins with a marker key (no "=" boundary) is kept —
	// only exact KEY=VALUE matches are stripped.
	in := []string{"RUNWISP_SERVICE_MANAGED_FOO=keep"}
	assert.Equal(t, in, WithoutServiceEnv(in))
}
