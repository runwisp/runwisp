// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package testutil

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/notify"
)

func TestFakeChannel(t *testing.T) {
	ch := NewFakeChannel("ch-id")
	if ch.ID() != "ch-id" {
		t.Fatalf("ID: got %q", ch.ID())
	}
	ev := &notify.Event{}

	if !ch.Match(ev) {
		t.Fatal("default Match should be true")
	}
	ch.MatchFn = func(*notify.Event) bool { return false }
	if ch.Match(ev) {
		t.Fatal("custom Match should win")
	}

	if err := ch.Execute(context.Background(), ev); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := ch.Received()
	if len(got) != 1 || got[0] != ev {
		t.Fatalf("Received: %v", got)
	}

	want := errors.New("boom")
	ch.Err = want
	if err := ch.Execute(context.Background(), ev); !errors.Is(err, want) {
		t.Fatalf("Execute err: got %v want %v", err, want)
	}

	if ch.Closed() {
		t.Fatal("Closed should be false initially")
	}
	if err := ch.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !ch.Closed() {
		t.Fatal("Closed should be true")
	}
}

func TestFakeClock(t *testing.T) {
	start := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	c := NewFakeClock(start)
	if !c.Now().Equal(start) {
		t.Fatalf("Now: got %v want %v", c.Now(), start)
	}
	c.Advance(2 * time.Hour)
	want := start.Add(2 * time.Hour)
	if !c.Now().Equal(want) {
		t.Fatalf("after Advance: got %v want %v", c.Now(), want)
	}
}

func TestFakeHub(t *testing.T) {
	h := NewFakeHub()
	if got := h.Captured(); len(got) != 0 {
		t.Fatalf("expected empty, got %v", got)
	}
	h.Publish("ev1", "payload-a")
	h.Publish("ev2", 42)

	cap := h.Captured()
	if len(cap) != 2 {
		t.Fatalf("len: got %d", len(cap))
	}
	if cap[0].Type != "ev1" || cap[0].Data != "payload-a" {
		t.Fatalf("first: %+v", cap[0])
	}
	if cap[1].Type != "ev2" || cap[1].Data != 42 {
		t.Fatalf("second: %+v", cap[1])
	}

	cap[0].Type = "mutated"
	if h.Captured()[0].Type != "ev1" {
		t.Fatal("Captured should return a copy")
	}
}
