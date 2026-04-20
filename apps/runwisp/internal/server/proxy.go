// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"strings"

	"github.com/sebest/xff"
)

// parseTrustedProxies parses RUNWISP_TRUST_PROXY as a comma-separated list of CIDR ranges.
// It converts it to xff.Options for the proxy middleware.
func parseTrustedProxies(env string) (*xff.Options, error) {
	if env == "" {
		return nil, nil
	}
	var subnets []string
	for _, raw := range strings.Split(env, ",") {
		cidr := strings.TrimSpace(raw)
		if cidr == "" {
			continue
		}
		if !strings.Contains(cidr, "/") {
			// Append a default /32 if it's an exact IP rather than CIDR.
			// XFF library expects full CIDRs on certain checks.
			if strings.Contains(cidr, ":") {
				cidr += "/128"
			} else {
				cidr += "/32"
			}
		}
		subnets = append(subnets, cidr)
	}

	if len(subnets) == 0 {
		return nil, nil
	}

	return &xff.Options{
		AllowedSubnets: subnets,
	}, nil
}
