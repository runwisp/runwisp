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
		{name: "systemd INVOCATION_ID fallback", managed: "", invocation: "5d10ec423bcf449789f2dfd36760a4ab", want: true},
		{name: "both set", managed: "1", invocation: "abc", want: true},
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
		"INVOCATION_ID=5d10ec423bcf449789f2dfd36760a4ab",
		"HOME=/home/op",
		"RUNWISP_SERVICE_MANAGED=1",
		"RUNWISP_DEMO_TEMP=/tmp/runwisp-demo-x",
	}
	got := WithoutServiceEnv(in)
	assert.Equal(t, []string{
		"PATH=/usr/bin",
		"HOME=/home/op",
		"RUNWISP_DEMO_TEMP=/tmp/runwisp-demo-x",
	}, got)
}

func TestWithoutServiceEnv_KeepsLookalikePrefixes(t *testing.T) {
	// A var that merely begins with a marker key (no "=" boundary) is kept —
	// only exact KEY=VALUE matches are stripped.
	in := []string{"INVOCATION_ID_EXTRA=keep", "RUNWISP_SERVICE_MANAGED_FOO=keep"}
	assert.Equal(t, in, WithoutServiceEnv(in))
}
