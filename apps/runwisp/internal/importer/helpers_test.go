// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package importer

import (
	"strings"
	"testing"
)

func TestIsAllDigits(t *testing.T) {
	cases := map[string]bool{
		"":      false,
		"0":     true,
		"12345": true,
		"1a2":   false,
		"-1":    false,
		" 1":    false,
	}
	for in, want := range cases {
		if got := isAllDigits(in); got != want {
			t.Errorf("isAllDigits(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestIsEnvAssignment(t *testing.T) {
	cases := map[string]bool{
		"FOO=bar":    true,
		"FOO=":       true,
		"=bar":       false, // no name
		"plaincmd":   false,
		"/usr/bin/x": false,
		"A=b/c":      true,  // '=' before any '/'
		"/opt/a=b":   false, // '/' before '=' → a path, not an assignment
	}
	for in, want := range cases {
		if got := isEnvAssignment(in); got != want {
			t.Errorf("isEnvAssignment(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestParseBool(t *testing.T) {
	truthy := []string{"true", "1", "yes", "on", "TRUE", " On "}
	for _, v := range truthy {
		if b, ok := parseBool(v); !ok || !b {
			t.Errorf("parseBool(%q) = (%v, %v), want (true, true)", v, b, ok)
		}
	}
	falsy := []string{"false", "0", "no", "off"}
	for _, v := range falsy {
		if b, ok := parseBool(v); !ok || b {
			t.Errorf("parseBool(%q) = (%v, %v), want (false, true)", v, b, ok)
		}
	}
	if _, ok := parseBool("maybe"); ok {
		t.Error("parseBool(maybe) should report not-ok")
	}
}

func TestAttentionCount(t *testing.T) {
	r := &Result{}
	r.addNote(LevelInfo, "a", "fyi")
	r.addNote(LevelAttention, "b", "look here")
	r.addNote(LevelAttention, "c", "and here")
	if got := r.AttentionCount(); got != 2 {
		t.Errorf("AttentionCount() = %d, want 2", got)
	}
}

func TestTomlStringMultiline(t *testing.T) {
	// A single-line value is a basic quoted string.
	if got := tomlString("echo hi"); got != `"echo hi"` {
		t.Errorf("tomlString single-line = %q", got)
	}
	// A multi-line value becomes a multi-line basic string with escaped
	// backslashes and a guarded embedded triple-quote.
	got := tomlString("line1\n\\path\n\"\"\"oops")
	if !strings.HasPrefix(got, "\"\"\"\n") || !strings.HasSuffix(got, "\"\"\"") {
		t.Errorf("multi-line tomlString missing triple-quote fences: %q", got)
	}
	if strings.Contains(got, `\path`) == false {
		t.Errorf("backslash should be escaped (doubled): %q", got)
	}
}

func TestServiceOnlyTaskDropsWithNote(t *testing.T) {
	sd := newSupervisordState()
	// On a service the key applies and no note is added.
	if !sd.serviceOnly("priority", "web", true) {
		t.Error("serviceOnly should return true for a service")
	}
	if len(sd.res.Notes) != 0 {
		t.Errorf("no note expected for a service, got %v", sd.res.Notes)
	}
	// On a run-once task it returns false and records why the key was dropped.
	if sd.serviceOnly("priority", "oneshot", false) {
		t.Error("serviceOnly should return false for a task")
	}
	if len(sd.res.Notes) != 1 || !strings.Contains(sd.res.Notes[0].Message, "run-once task") {
		t.Errorf("expected a dropped-key note, got %v", sd.res.Notes)
	}
}
