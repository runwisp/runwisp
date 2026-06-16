// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package model

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sp(s string) *string { return &s }

func TestResolveParamValues_DefaultsAndOmission(t *testing.T) {
	params := []TaskParam{
		{Kind: ParamArg, Key: "source", Required: true},
		{Kind: ParamArg, Key: "dest", Default: sp("/backups")},
		{Kind: ParamOption, Key: "--date-from"}, // optional, no default
		{Kind: ParamFlag, Key: "--force"},
	}

	resolved, err := ResolveParamValues(params, map[string]*string{"source": sp("/data")})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"source":  "/data",
		"dest":    "/backups",
		"--force": "false",
	}, resolved, "optional unset value param is omitted; flag defaults false")
}

func TestResolveParamValues_ExplicitOmitDropsDefault(t *testing.T) {
	params := []TaskParam{
		{Kind: ParamArg, Key: "dest", Default: sp("/backups")},
		{Kind: ParamFlag, Key: "--force"},
	}

	// A nil pointer is an explicit omit: the declared default must NOT be
	// re-injected. This is the manual-trigger bug — clearing a defaulted field
	// has to drop the parameter, not fall back to its default.
	resolved, err := ResolveParamValues(params, map[string]*string{"dest": nil})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"--force": "false"}, resolved,
		"explicit nil omits the param even when it has a default")
}

func TestResolveParamValues_ExplicitOmitRequiredIsError(t *testing.T) {
	params := []TaskParam{{Kind: ParamArg, Key: "source", Required: true, Default: sp("/data")}}
	_, err := ResolveParamValues(params, map[string]*string{"source": nil})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "required", "a required param cannot be explicitly omitted")
}

func TestResolveParamValues_EmptyStringIsPassed(t *testing.T) {
	params := []TaskParam{{Kind: ParamOption, Key: "--note", Default: sp("hi")}}

	// An explicit empty string is distinct from omit: it overrides the default
	// and passes "" through to the run.
	resolved, err := ResolveParamValues(params, map[string]*string{"--note": sp("")})
	require.NoError(t, err)
	val, ok := resolved["--note"]
	require.True(t, ok, "empty string is present in the resolved map")
	assert.Equal(t, "", val)
}

func TestResolveParamValues_AbsentKeyKeepsDefault(t *testing.T) {
	params := []TaskParam{{Kind: ParamArg, Key: "dest", Default: sp("/backups")}}

	// An absent key (the partial-merge / scheduled path) still applies the
	// default — only an explicit nil omits.
	resolved, err := ResolveParamValues(params, map[string]*string{})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"dest": "/backups"}, resolved)
}

func TestResolveParamValues_RequiredMissing(t *testing.T) {
	params := []TaskParam{{Kind: ParamArg, Key: "source", Required: true}}
	_, err := ResolveParamValues(params, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "required")
}

func TestResolveParamValues_UnknownKeyRejected(t *testing.T) {
	params := []TaskParam{{Kind: ParamEnv, Key: "FOO"}}
	_, err := ResolveParamValues(params, map[string]*string{"BAR": sp("x")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown parameter")
}

func TestResolveParamValues_EnumEnforcement(t *testing.T) {
	params := []TaskParam{{Kind: ParamOption, Key: "--region", Choices: []string{"us", "eu"}}}

	_, err := ResolveParamValues(params, map[string]*string{"--region": sp("ap")})
	require.Error(t, err)

	resolved, err := ResolveParamValues(params, map[string]*string{"--region": sp("eu")})
	require.NoError(t, err)
	assert.Equal(t, "eu", resolved["--region"])
}

func TestResolveParamValues_AllowCustomBypassesEnum(t *testing.T) {
	params := []TaskParam{{Kind: ParamOption, Key: "--tag", Choices: []string{"a", "b"}, AllowCustom: true}}
	resolved, err := ResolveParamValues(params, map[string]*string{"--tag": sp("custom")})
	require.NoError(t, err)
	assert.Equal(t, "custom", resolved["--tag"])
}

func TestResolveParamValues_NumberParsing(t *testing.T) {
	params := []TaskParam{{Kind: ParamOption, Key: "--limit", Type: ParamTypeNumber}}

	_, err := ResolveParamValues(params, map[string]*string{"--limit": sp("abc")})
	require.Error(t, err)

	resolved, err := ResolveParamValues(params, map[string]*string{"--limit": sp("42")})
	require.NoError(t, err)
	assert.Equal(t, "42", resolved["--limit"])
}

func TestResolveParamValues_FlagCanonicalisation(t *testing.T) {
	params := []TaskParam{{Kind: ParamFlag, Key: "--force"}}

	resolved, err := ResolveParamValues(params, map[string]*string{"--force": sp("1")})
	require.NoError(t, err)
	assert.Equal(t, "true", resolved["--force"], "truthy values canonicalise to true")

	_, err = ResolveParamValues(params, map[string]*string{"--force": sp("maybe")})
	require.Error(t, err)
}

func TestResolveParamValues_SuppliedNULRejected(t *testing.T) {
	params := []TaskParam{{Kind: ParamArg, Key: "source"}}
	_, err := ResolveParamValues(params, map[string]*string{"source": sp("a\x00b")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "NUL")
}

func TestResolveParamValues_SuppliedOversizeRejected(t *testing.T) {
	params := []TaskParam{{Kind: ParamEnv, Key: "DATA"}}
	big := strings.Repeat("x", EnvMaxValueLen+1)
	_, err := ResolveParamValues(params, map[string]*string{"DATA": sp(big)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cap is")
}

func TestParamEnvLayer_OnlyEnvKind(t *testing.T) {
	params := []TaskParam{
		{Kind: ParamEnv, Key: "PROJECT_ID"},
		{Kind: ParamArg, Key: "source"},
	}
	resolved := map[string]string{"PROJECT_ID": "acme", "source": "/data"}
	layer := ParamEnvLayer(params, resolved)
	assert.Equal(t, map[string]string{"PROJECT_ID": "acme"}, layer)
}

func TestParamEnvLayer_NilWhenNoEnvParams(t *testing.T) {
	params := []TaskParam{{Kind: ParamArg, Key: "source"}}
	layer := ParamEnvLayer(params, map[string]string{"source": "/data"})
	assert.Nil(t, layer)
}

func TestParamArgTokens_OrderAndShapes(t *testing.T) {
	params := []TaskParam{
		{Kind: ParamArg, Key: "source"},
		{Kind: ParamOption, Key: "--region"},
		{Kind: ParamOption, Key: "--date="}, // trailing = → joined form
		{Kind: ParamFlag, Key: "--force"},
		{Kind: ParamEnv, Key: "PROJECT_ID"}, // never an argv token
	}
	resolved := map[string]string{
		"source":     "/data",
		"--region":   "eu",
		"--date=":    "2026-01-01",
		"--force":    "true",
		"PROJECT_ID": "acme",
	}
	tokens := ParamArgTokens(params, resolved)
	assert.Equal(t, []string{
		"/data",
		"--region", "eu",
		"--date=2026-01-01",
		"--force",
	}, tokens)
}

func TestParamArgTokens_FlagOffAndUnsetOmitted(t *testing.T) {
	params := []TaskParam{
		{Kind: ParamOption, Key: "--date-from"}, // unset
		{Kind: ParamFlag, Key: "--force"},       // off
	}
	resolved := map[string]string{"--force": "false"}
	assert.Empty(t, ParamArgTokens(params, resolved))
}
