// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runtime"
	"github.com/runwisp/runwisp/internal/server"
	"github.com/spf13/cobra"
)

var openapiCmd = &cobra.Command{
	Use:   "openapi",
	Short: "Print the OpenAPI 3.1 spec (JSON) to stdout",
	Long:  `Generates and prints the OpenAPI 3.1 specification derived from the huma route definitions. Useful for feeding into code generators (openapi-typescript, openapi-zod-client, etc.).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runOpenAPI()
	},
}

func runOpenAPI() error {
	spec, err := openAPISpecJSON()
	if err != nil {
		return err
	}
	fmt.Println(string(spec))
	return nil
}

// openAPISpecJSON builds a server with minimal dependencies (only route
// registration matters) and returns its OpenAPI 3.1 spec as indented JSON. It
// is the single source of the spec bytes, shared by `runwisp openapi` and the
// drift test that guards the committed openapi.json.
func openAPISpecJSON() ([]byte, error) {
	srv, err := server.New(server.Options{
		Tasks:      runtime.NewTaskRegistry(nil),
		Port:       9477,
		LogDir:     os.TempDir(),
		EventBus:   events.NewEventBus(),
		Password:   "openapi-generation",
		JWTSecret:  "openapi-generation",
		DaemonInfo: &model.DaemonInfo{},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to construct server for OpenAPI generation: %w", err)
	}
	spec, err := json.MarshalIndent(srv.API().OpenAPI(), "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal OpenAPI spec: %w", err)
	}
	return spec, nil
}
