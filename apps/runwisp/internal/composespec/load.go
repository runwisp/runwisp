// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package composespec

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/compose-spec/compose-go/v2/cli"
	"github.com/compose-spec/compose-go/v2/loader"
)

// Load parses a docker compose file and returns a minimal Project. profiles
// filters services to those tagged with the named profiles (compose semantics).
// envFiles is appended to compose's normal .env discovery for variable
// interpolation. workingDir defaults to the directory of file when empty.
//
// No docker daemon is contacted — this is pure parsing, safe to run offline.
func Load(file string, profiles, envFiles []string, workingDir string) (*Project, error) {
	if file == "" {
		return nil, fmt.Errorf("compose file path is required")
	}
	absFile, err := filepath.Abs(file)
	if err != nil {
		return nil, fmt.Errorf("resolve compose file path: %w", err)
	}
	if workingDir == "" {
		workingDir = filepath.Dir(absFile)
	}

	projectOpts, err := cli.NewProjectOptions(
		[]string{absFile},
		cli.WithWorkingDirectory(workingDir),
		cli.WithProfiles(profiles),
		cli.WithEnvFiles(envFiles...),
		cli.WithOsEnv,
		cli.WithDotEnv,
		// We only care about service names + grace periods; skip the strict
		// consistency check so a partially-broken file still enumerates.
		cli.WithLoadOptions(func(o *loader.Options) {
			o.SkipConsistencyCheck = true
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("build compose project options: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	project, err := projectOpts.LoadProject(ctx)
	if err != nil {
		return nil, fmt.Errorf("load compose file %s: %w", file, err)
	}

	names := make([]string, 0, len(project.Services))
	for name := range project.Services {
		names = append(names, name)
	}
	sort.Strings(names)

	services := make([]Service, 0, len(names))
	for _, name := range names {
		svc := project.Services[name]
		var grace time.Duration
		if svc.StopGracePeriod != nil {
			grace = time.Duration(*svc.StopGracePeriod)
		}
		services = append(services, Service{
			Name:            name,
			StopGracePeriod: grace,
		})
	}

	return &Project{Services: services}, nil
}
