// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package cloud

import (
	"encoding/json"
	"testing"

	"github.com/runwisp/runwisp/internal/generated/protocol"
)

func TestExecutionStatusJSON(t *testing.T) {
	cases := []struct {
		val  protocol.ExecutionStatus
		want string
	}{
		{protocol.ExecutionStatusRunning, `"running"`},
		{protocol.ExecutionStatusOk, `"ok"`},
		{protocol.ExecutionStatusErr, `"err"`},
		{protocol.ExecutionStatusStopped, `"stopped"`},
		{protocol.ExecutionStatusTimeout, `"timeout"`},
	}
	for _, c := range cases {
		got, err := json.Marshal(c.val)
		if err != nil {
			t.Fatalf("marshal %v: %v", c.val, err)
		}
		if string(got) != c.want {
			t.Fatalf("marshal %v: got %s, want %s", c.val, got, c.want)
		}
		var rt protocol.ExecutionStatus
		if err := json.Unmarshal(got, &rt); err != nil {
			t.Fatalf("unmarshal %s: %v", got, err)
		}
		if rt != c.val {
			t.Fatalf("round-trip mismatch: got %v want %v", rt, c.val)
		}
	}

	if v := protocol.ExecutionStatus(99).Value(); v != nil {
		t.Fatalf("out-of-range Value: got %v want nil", v)
	}
	var bad protocol.ExecutionStatus
	if err := bad.UnmarshalJSON([]byte("not json")); err == nil {
		t.Fatal("expected error on invalid json")
	}
}

func TestStreamJSON(t *testing.T) {
	cases := []struct {
		val  protocol.Stream
		want string
	}{
		{protocol.StreamStdout, `"stdout"`},
		{protocol.StreamStderr, `"stderr"`},
		{protocol.StreamSystem, `"system"`},
	}
	for _, c := range cases {
		got, err := json.Marshal(c.val)
		if err != nil {
			t.Fatalf("marshal %v: %v", c.val, err)
		}
		if string(got) != c.want {
			t.Fatalf("marshal %v: got %s, want %s", c.val, got, c.want)
		}
		var rt protocol.Stream
		if err := json.Unmarshal(got, &rt); err != nil {
			t.Fatalf("unmarshal %s: %v", got, err)
		}
		if rt != c.val {
			t.Fatalf("round-trip mismatch: got %v want %v", rt, c.val)
		}
	}
	if v := protocol.Stream(99).Value(); v != nil {
		t.Fatalf("out-of-range Value: got %v want nil", v)
	}
	var bad protocol.Stream
	if err := bad.UnmarshalJSON([]byte("{")); err == nil {
		t.Fatal("expected error on invalid json")
	}
}

func TestLinesItemStreamJSON(t *testing.T) {
	cases := []struct {
		val  protocol.LinesItemStream
		want string
	}{
		{protocol.LinesItemStreamStdout, `"stdout"`},
		{protocol.LinesItemStreamStderr, `"stderr"`},
		{protocol.LinesItemStreamSystem, `"system"`},
	}
	for _, c := range cases {
		got, err := json.Marshal(c.val)
		if err != nil {
			t.Fatalf("marshal %v: %v", c.val, err)
		}
		if string(got) != c.want {
			t.Fatalf("marshal %v: got %s, want %s", c.val, got, c.want)
		}
		var rt protocol.LinesItemStream
		if err := json.Unmarshal(got, &rt); err != nil {
			t.Fatalf("unmarshal %s: %v", got, err)
		}
		if rt != c.val {
			t.Fatalf("round-trip mismatch: got %v want %v", rt, c.val)
		}
	}
	if v := protocol.LinesItemStream(99).Value(); v != nil {
		t.Fatalf("out-of-range Value: got %v want nil", v)
	}
	var bad protocol.LinesItemStream
	if err := bad.UnmarshalJSON([]byte("[")); err == nil {
		t.Fatal("expected error on invalid json")
	}
}

func TestTriggerTypeJSON(t *testing.T) {
	cases := []struct {
		val  protocol.TriggerType
		want string
	}{
		{protocol.TriggerTypeManual, `"manual"`},
		{protocol.TriggerTypeSchedule, `"schedule"`},
		{protocol.TriggerTypeSuccess, `"success"`},
		{protocol.TriggerTypeFailure, `"failure"`},
	}
	for _, c := range cases {
		got, err := json.Marshal(c.val)
		if err != nil {
			t.Fatalf("marshal %v: %v", c.val, err)
		}
		if string(got) != c.want {
			t.Fatalf("marshal %v: got %s, want %s", c.val, got, c.want)
		}
		var rt protocol.TriggerType
		if err := json.Unmarshal(got, &rt); err != nil {
			t.Fatalf("unmarshal %s: %v", got, err)
		}
		if rt != c.val {
			t.Fatalf("round-trip mismatch: got %v want %v", rt, c.val)
		}
	}
	if v := protocol.TriggerType(99).Value(); v != nil {
		t.Fatalf("out-of-range Value: got %v want nil", v)
	}
	var bad protocol.TriggerType
	if err := bad.UnmarshalJSON([]byte("]")); err == nil {
		t.Fatal("expected error on invalid json")
	}
}
