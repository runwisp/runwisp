// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package model

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// ParamKind identifies how a declared task parameter renders into the run. The
// kind doubles as the TOML identity keyword (`env` / `arg` / `option` / `flag`)
// and is derived from which of those keys the operator set in runwisp.toml.
type ParamKind string

const (
	// ParamEnv injects NAME=value into the run's process environment.
	ParamEnv ParamKind = "env"
	// ParamArg appends the bare value as a positional argument, in order.
	ParamArg ParamKind = "arg"
	// ParamOption appends `--name value` (or `--name=value` when the option
	// string ends in `=`).
	ParamOption ParamKind = "option"
	// ParamFlag appends the flag token when on, nothing when off.
	ParamFlag ParamKind = "flag"
)

// Param value types. The default is string; number adds numeric coercion at
// resolve time. Boolean is implied by ParamFlag, enum by a non-empty Choices.
const (
	ParamTypeString = "string"
	ParamTypeNumber = "number"
)

// EnvMaxValueLen caps a single env value — and any operator-supplied parameter
// value, since those also flow into the run's argv/env — at 32 KiB. Linux's
// argv+env limit is 128 KiB by default; capping per-value leaves room for many
// entries without bumping into ARG_MAX surprises. The config package re-uses
// this for its env-map validation so the two limits never drift.
const EnvMaxValueLen = 32 * 1024

// TaskParam declares one per-execution parameter on a task. Declarations come
// from runwisp.toml only (the sole source of truth); manual trigger surfaces
// supply *values* for them but never define them.
type TaskParam struct {
	Kind ParamKind `json:"kind" enum:"env,arg,option,flag" doc:"How the parameter renders into the run"`
	// Key is the parameter's canonical identity: the env var name, the
	// positional label, or the literal option/flag token (e.g. "--force").
	// It is the key used in the trigger body, run history, and as the UI field id.
	Key         string   `json:"key" doc:"Canonical parameter key (env name, positional label, or option/flag token)"`
	Type        string   `json:"type,omitempty" enum:"string,number" doc:"Value type; defaults to string"`
	Default     *string  `json:"default,omitempty" doc:"Default value used by scheduled runs and pre-filled in manual forms"`
	Required    bool     `json:"required,omitempty" doc:"Whether a manual trigger must supply a value"`
	Choices     []string `json:"choices,omitempty" doc:"Allowed values; renders as a dropdown"`
	AllowCustom bool     `json:"allow_custom,omitempty" doc:"When choices is set, allow values outside the list"`
	Description string   `json:"description,omitempty" doc:"Help text shown under the field"`
}

// ResolveParamValues validates operator-supplied values against the task's
// parameter declarations, applies defaults, coerces/canonicalises, and returns
// the persisted identity→value map. It is a pure transform — no clock, no I/O.
//
// The supplied map carries three distinct states per key, so a manual trigger
// can express more than "value or default":
//
//   - key absent → use the declared default (scheduled firings pass nil to get a
//     defaults-only map; a REST caller can send only the keys it overrides).
//   - key present, nil → explicitly omit: the parameter is not passed at all,
//     even if it has a default (a required param omitted this way is an error).
//   - key present, non-nil → pass that exact value, including the empty string.
//
// Other rules:
//
//   - Unknown supplied key → error.
//   - Missing required with no default → error.
//   - Enum value outside choices (unless allow_custom) → error.
//   - number that does not parse → error.
//
// Flags always appear in the resolved map as "true"/"false". Optional value
// params that are absent-with-no-default or explicitly omitted contribute no key,
// so run history shows only what actually took effect.
func ResolveParamValues(params []TaskParam, supplied map[string]*string) (map[string]string, error) {
	byKey := make(map[string]TaskParam, len(params))
	for _, p := range params {
		byKey[p.Key] = p
	}
	for key := range supplied {
		if _, ok := byKey[key]; !ok {
			return nil, fmt.Errorf("unknown parameter %q", key)
		}
	}

	resolved := make(map[string]string, len(params))
	for _, p := range params {
		raw, given := supplied[p.Key]
		val, present, err := resolveParam(p, raw, given)
		if err != nil {
			return nil, err
		}
		if present {
			resolved[p.Key] = val
		}
	}
	return resolved, nil
}

// resolveParam resolves a single parameter to its final value. present reports
// whether the value belongs in the resolved map: flags are always present; value
// params are absent when they have no default, are explicitly omitted (nil), or
// resolve to no value. raw is the supplied pointer (nil = explicit omit), given
// reports whether the key was present in the supplied map at all.
func resolveParam(p TaskParam, raw *string, given bool) (value string, present bool, err error) {
	if p.Kind == ParamFlag {
		val, err := resolveFlagValue(p, raw)
		if err != nil {
			return "", false, err
		}
		return val, true, nil
	}
	// Explicit omit: the key was sent as null. Drop the parameter even when it
	// has a default; only a required param can't be omitted.
	if given && raw == nil {
		if p.Required {
			return "", false, fmt.Errorf("parameter %q is required", p.Key)
		}
		return "", false, nil
	}
	if !given {
		if p.Default != nil {
			return *p.Default, true, nil
		}
		if p.Required {
			return "", false, fmt.Errorf("parameter %q is required", p.Key)
		}
		return "", false, nil
	}
	if err := validateParamValue(p, *raw); err != nil {
		return "", false, err
	}
	return *raw, true, nil
}

// resolveFlagValue canonicalises a flag to "true"/"false", honouring the
// declared default when no value is supplied. A nil pointer (absent or explicit
// null) means "not supplied" — flags have no third state.
func resolveFlagValue(p TaskParam, raw *string) (string, error) {
	if raw == nil {
		if p.Default != nil {
			return *p.Default, nil
		}
		return "false", nil
	}
	b, err := strconv.ParseBool(*raw)
	if err != nil {
		return "", fmt.Errorf("parameter %q expects a boolean, got %q", p.Key, *raw)
	}
	return strconv.FormatBool(b), nil
}

// validateParamValue enforces the shared NUL/length guards plus enum membership
// and number parsing on a supplied value for a value-bearing parameter. The
// NUL/length checks mirror what declared defaults get at config load, so a
// supplied value can't slip past them into a failed spawn.
func validateParamValue(p TaskParam, value string) error {
	if strings.ContainsRune(value, 0) {
		return fmt.Errorf("parameter %q value contains a NUL byte", p.Key)
	}
	if len(value) > EnvMaxValueLen {
		return fmt.Errorf("parameter %q value is %d bytes; cap is %d", p.Key, len(value), EnvMaxValueLen)
	}
	if len(p.Choices) > 0 && !p.AllowCustom {
		if slices.Contains(p.Choices, value) {
			return nil
		}
		return fmt.Errorf("parameter %q value %q is not one of %s", p.Key, value, strings.Join(p.Choices, ", "))
	}
	if p.Type == ParamTypeNumber {
		if _, err := strconv.ParseFloat(value, 64); err != nil {
			return fmt.Errorf("parameter %q expects a number, got %q", p.Key, value)
		}
	}
	return nil
}

// SuppliedFromResolved turns a run's resolved identity→value map into an
// authoritative supplied map, for re-running it exactly (retry, restart, rerun).
// Every declared parameter present in resolved maps to that exact value, and any
// declared parameter absent from resolved maps to an explicit nil — so a value
// the operator deliberately omitted stays omitted on the re-resolve instead of
// picking its declared default back up.
//
// A nil resolved map (a scheduled/defaults-only run carries no params) returns
// nil, so the re-trigger resolves to defaults again.
func SuppliedFromResolved(params []TaskParam, resolved map[string]string) map[string]*string {
	if resolved == nil {
		return nil
	}
	out := make(map[string]*string, len(params))
	for _, p := range params {
		if v, ok := resolved[p.Key]; ok {
			vv := v
			out[p.Key] = &vv
		} else {
			out[p.Key] = nil // explicit omit
		}
	}
	return out
}

// PointerValues lifts a plain identity→value map into the supplied-map shape
// ResolveParamValues expects, with every value present (non-nil). It is for
// surfaces whose wire type can't carry the explicit-omit (nil) state — the cloud
// control-plane protocol — where an absent key still means "use the default".
func PointerValues(m map[string]string) map[string]*string {
	if m == nil {
		return nil
	}
	out := make(map[string]*string, len(m))
	for k, v := range m {
		vv := v
		out[k] = &vv
	}
	return out
}

// ParamEnvLayer returns the env-kind parameters as a KEY=VALUE overlay map,
// ready to layer last over task.Env/task.Secrets in the process environment.
func ParamEnvLayer(params []TaskParam, resolved map[string]string) map[string]string {
	var out map[string]string
	for _, p := range params {
		if p.Kind != ParamEnv {
			continue
		}
		if v, ok := resolved[p.Key]; ok {
			if out == nil {
				out = make(map[string]string)
			}
			out[p.Key] = v
		}
	}
	return out
}

// ParamArgTokens returns the arg/option/flag parameters as argv tokens in
// declaration order. A flag that is off, or an optional value param that was
// never set, contributes no token. The tokens are plain argv: callers that
// splice them into script text quote them first (the host shell backend, and
// compose exec mode, both via executor.appendArgTokens), while compose run mode
// passes them as the container's real argv where no quoting applies.
func ParamArgTokens(params []TaskParam, resolved map[string]string) []string {
	var tokens []string
	for _, p := range params {
		switch p.Kind {
		case ParamArg:
			if v, ok := resolved[p.Key]; ok {
				tokens = append(tokens, v)
			}
		case ParamOption:
			if v, ok := resolved[p.Key]; ok {
				tokens = append(tokens, optionTokens(p.Key, v)...)
			}
		case ParamFlag:
			if resolved[p.Key] == "true" {
				tokens = append(tokens, p.Key)
			}
		}
	}
	return tokens
}

// optionTokens renders a resolved option parameter as argv tokens. A key ending
// in "=" glues the value on (`--opt=val`); otherwise the value is a separate
// token (`--opt val`).
func optionTokens(key, v string) []string {
	if strings.HasSuffix(key, "=") {
		return []string{key + v}
	}
	return []string{key, v}
}
