// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestNewUpdateChecker_ZeroValueStatus(t *testing.T) {
	c := NewUpdateChecker("0.16.0", http.DefaultClient)
	available, latest := c.Status()
	if available || latest != "" {
		t.Errorf("expected zero-value status before any check, got available=%v latest=%q", available, latest)
	}
}

func TestUpdateChecker_StartRunsAnInitialCheck(t *testing.T) {
	c := NewUpdateChecker("0.16.0", http.DefaultClient)
	// Swap the fetch stub in before Start so the background goroutine never
	// touches the network.
	c.fetch = func(context.Context) (string, error) { return "v0.17.0", nil }
	c.Start()
	t.Cleanup(c.Stop)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if available, latest := c.Status(); available {
			if latest != "v0.17.0" {
				t.Fatalf("latest=%q want v0.17.0", latest)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("Start did not run its initial check in time")
}

func TestUpdateChecker_StopBeforeStartIsSafe(t *testing.T) {
	c := NewUpdateChecker("0.16.0", http.DefaultClient)
	c.Stop() // cancel is nil until Start(); must not panic
}

func TestUpdateCheckerAvailable(t *testing.T) {
	c := &UpdateChecker{
		current: "0.16.0",
		fetch:   func(context.Context) (string, error) { return "v0.17.0", nil },
	}
	c.checkOnce(context.Background())

	available, latest := c.Status()
	if !available {
		t.Error("expected update available")
	}
	if latest != "v0.17.0" {
		t.Errorf("latest=%q want v0.17.0", latest)
	}
}

func TestUpdateCheckerUpToDate(t *testing.T) {
	c := &UpdateChecker{
		current: "0.17.0",
		fetch:   func(context.Context) (string, error) { return "v0.17.0", nil },
	}
	c.checkOnce(context.Background())

	if available, _ := c.Status(); available {
		t.Error("did not expect an update when on the latest version")
	}
}

func TestUpdateCheckerFetchErrorKeepsLastGood(t *testing.T) {
	c := &UpdateChecker{
		current: "0.16.0",
		fetch:   func(context.Context) (string, error) { return "v0.17.0", nil },
	}
	c.checkOnce(context.Background()) // seed a good result

	c.fetch = func(context.Context) (string, error) { return "", errors.New("offline") }
	c.checkOnce(context.Background()) // must not clobber the cached status

	available, latest := c.Status()
	if !available || latest != "v0.17.0" {
		t.Errorf("failed check overwrote cached status: available=%v latest=%q", available, latest)
	}
}
