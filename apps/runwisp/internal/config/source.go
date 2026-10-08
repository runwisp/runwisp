// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"os"
	"sync"
)

// Source is where a daemon's config comes from: a runwisp.toml on disk, or the
// document an attached app supplies. Boot and every reload load it through
// here, so the two take the same steps.
type Source interface {
	// Path is the root config's location: the runwisp.toml, or the file an
	// app's document stands in for. Relative paths resolve against its
	// directory.
	Path() string
	Load() (*Config, error)
}

// FileSource is a runwisp.toml on disk.
type FileSource string

func (s FileSource) Path() string { return string(s) }

func (s FileSource) Load() (*Config, error) {
	cfg, err := Load(string(s))
	if err != nil {
		return nil, err
	}
	// A root daemon executes whatever the config says; re-assert the file
	// (and its includes) are not reachable through a user-writable path or a
	// repointable symlink before trusting it. No-op when unprivileged.
	if err := AssertPrivilegedConfigTrust(cfg, string(s)); err != nil {
		return nil, err
	}
	return cfg, ApplyTrustedProxiesEnv(cfg)
}

// DocumentSource is the config an attached app supplies as JSON (see
// LoadDocument). Set replaces the document; like an edited runwisp.toml, it
// takes effect at the next Load, and a document a reload rejected stays the
// current one until the app sends another.
type DocumentSource struct {
	path string

	mu  sync.Mutex
	doc []byte
}

// NewDocumentSource returns a source whose document stands in for the file at
// path.
func NewDocumentSource(path string, doc []byte) *DocumentSource {
	return &DocumentSource{path: path, doc: doc}
}

func (s *DocumentSource) Path() string { return s.path }

// Set replaces the document.
func (s *DocumentSource) Set(doc []byte) {
	s.mu.Lock()
	s.doc = doc
	s.mu.Unlock()
}

func (s *DocumentSource) Load() (*Config, error) {
	s.mu.Lock()
	doc := s.doc
	s.mu.Unlock()
	cfg, err := LoadDocument(s.path, doc)
	if err != nil {
		return nil, err
	}
	// The document arrives over the daemon's local socket from its own user,
	// trusted like the daemon's own config file; files it includes are read
	// from disk and get the same check a runwisp.toml's includes do.
	if err := assertIncludesTrusted(cfg, os.Geteuid()); err != nil {
		return nil, err
	}
	return cfg, ApplyTrustedProxiesEnv(cfg)
}
