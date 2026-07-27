// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package composespec is the only consumer of github.com/compose-spec/compose-go/v2
// in the codebase. It exposes a minimal Project/Service surface so the rest of
// the daemon stays unaware of the upstream library's much larger type tree.
package composespec

import "time"

// Project is the trimmed-down view of a parsed compose file that the rest of
// the daemon cares about.
type Project struct {
	// Services holds the resolved services in the file, post-profile filtering,
	// in the order returned by the loader.
	Services []Service
}

// Service is the trimmed-down view of a single compose service.
type Service struct {
	Name string
	// StopGracePeriod is the compose-declared grace window. Zero means the
	// compose file did not set one — RunWisp will fall back to its own default.
	StopGracePeriod time.Duration
}

// ServiceNames returns just the service names, in loader order. Convenience
// helper for callers that only need the list (e.g. include/exclude validation).
func (p *Project) ServiceNames() []string {
	out := make([]string, len(p.Services))
	for i, s := range p.Services {
		out[i] = s.Name
	}
	return out
}

// Service looks up a service by name. Returns nil when not present.
func (p *Project) Service(name string) *Service {
	for i := range p.Services {
		if p.Services[i].Name == name {
			return &p.Services[i]
		}
	}
	return nil
}
