// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"errors"
	"testing"
)

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
