// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package testutil

import (
	"context"
	"errors"
	"testing"

	"github.com/runwisp/runwisp/apps/runwisp/internal/notify"
)

func TestFakeChannel(t *testing.T) {
	ch := NewFakeChannel("ch-id")
	if ch.ID() != "ch-id" {
		t.Fatalf("ID: got %q", ch.ID())
	}
	ev := &notify.Event{}

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
