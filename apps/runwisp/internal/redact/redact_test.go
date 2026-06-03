// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package redact

import (
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRedactor_MasksRegisteredValue(t *testing.T) {
	r := New()
	r.Add("super-secret-token")
	got := r.Redact("the token is super-secret-token, keep it safe")
	assert.Equal(t, "the token is [redacted], keep it safe", got)
}

func TestRedactor_MasksAllOccurrences(t *testing.T) {
	r := New()
	r.Add("hunter2pw")
	got := r.Redact("hunter2pw hunter2pw")
	assert.Equal(t, "[redacted] [redacted]", got)
}

func TestRedactor_MultipleValues(t *testing.T) {
	r := New()
	r.Add("first-secret")
	r.Add("second-secret")
	got := r.Redact("a=first-secret b=second-secret")
	assert.Equal(t, "a=[redacted] b=[redacted]", got)
}

func TestRedactor_IgnoresShortValues(t *testing.T) {
	r := New()
	r.Add("abc") // below the floor
	assert.Equal(t, "abc def", r.Redact("abc def"))
}

func TestRedactor_NoValuesIsPassthrough(t *testing.T) {
	r := New()
	assert.Equal(t, "untouched", r.Redact("untouched"))
}

func TestRedactor_NilIsNoOp(t *testing.T) {
	var r *Redactor
	r.Add("secret-value") // must not panic
	assert.Equal(t, "secret-value", r.Redact("secret-value"))
}

func TestRedactor_AddIsIdempotent(t *testing.T) {
	r := New()
	r.Add("repeat-secret")
	r.Add("repeat-secret")
	assert.Equal(t, "[redacted]", r.Redact("repeat-secret"))
}

func TestRedactor_ConcurrentAddAndRedact(t *testing.T) {
	r := New()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func(n int) { defer wg.Done(); r.Add("secret-" + strings.Repeat("x", n+4)) }(i)
		go func() { defer wg.Done(); _ = r.Redact("scanning for secret-xxxx values") }()
	}
	wg.Wait()
	assert.Contains(t, r.Redact("secret-xxxx"), "[redacted]")
}
