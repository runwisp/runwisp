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
