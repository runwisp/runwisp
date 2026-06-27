// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package apiclient

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/runwisp/runwisp/internal/tlscert"
)

type fakeStore struct{ m map[string]string }

func newFakeStore() *fakeStore { return &fakeStore{m: map[string]string{}} }

func (f *fakeStore) Load(k string) (string, bool) { v, ok := f.m[k]; return v, ok }
func (f *fakeStore) Store(k, v string)            { f.m[k] = v }

func TestNewPinned_RecordsPinOnFirstUse(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	store := newFakeStore()
	c := NewPinned(srv.URL, "", store)
	if err := c.HealthCheck(); err != nil {
		t.Fatalf("first connect should succeed (TOFU): %v", err)
	}

	want := tlscert.FingerprintDER(srv.Certificate().Raw)
	if got, ok := store.Load(NormalizeBaseURL(srv.URL)); !ok || got != want {
		t.Fatalf("pin not recorded: got %q ok=%v, want %q", got, ok, want)
	}

	// A second connection with the same cert still succeeds.
	if err := c.HealthCheck(); err != nil {
		t.Fatalf("matching pin should still connect: %v", err)
	}
}

func TestNewPinned_RejectsChangedCert(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Pre-seed a pin for this URL that doesn't match the server's actual cert,
	// simulating a cert that changed since first connect.
	store := &fakeStore{m: map[string]string{NormalizeBaseURL(srv.URL): "00deadbeef"}}
	c := NewPinned(srv.URL, "", store)

	err := c.HealthCheck()
	if err == nil {
		t.Fatal("expected a pin mismatch error, got nil")
	}
	var mismatch *CertPinMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("expected *CertPinMismatchError, got %T: %v", err, err)
	}
	if mismatch.Pinned != "00deadbeef" {
		t.Fatalf("mismatch.Pinned = %q, want the pre-seeded pin", mismatch.Pinned)
	}
}
