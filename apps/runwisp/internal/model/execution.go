// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package model

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ExecutionDef is a sealed-type discriminant; ExecType() identifies the concrete
// subtype for JSON deserialization. The single-method interface is intentional —
// it exists only to constrain the type set, not as a behavioural abstraction.
type ExecutionDef interface { //NOSONAR: sealed-type discriminant, not a behavioural interface
	ExecType() string
}

// KeyValue is a generic key-value pair used for headers, env vars, etc.
type KeyValue struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// --- Shell execution ---

// ShellExecution runs a script on the host via /bin/sh.
type ShellExecution struct {
	Script string `json:"script"`
}

func (e *ShellExecution) ExecType() string { return "shell" }

// --- Container execution ---

// DockerfileBlock is a user-editable Dockerfile instruction group.
type DockerfileBlock struct {
	ID              string                 `json:"id"`
	Label           string                 `json:"label"`
	Reason          string                 `json:"reason"`
	Dockerfile      string                 `json:"dockerfile"`
	Enabled         bool                   `json:"enabled"`
	AutoDetected    bool                   `json:"auto_detected"`
	Modified        bool                   `json:"modified"`
	Pinned          bool                   `json:"pinned"`
	ManagedPackages *DockerManagedPackages `json:"managed_packages,omitempty"`
}

// DockerManagedPackages holds package names for managed install blocks.
type DockerManagedPackages struct {
	Apk string `json:"apk,omitempty"`
	Apt string `json:"apt,omitempty"`
}

// VolumeMount maps a host path into the container.
type VolumeMount struct {
	Host      string `json:"host"`
	Container string `json:"container"`
	ReadOnly  bool   `json:"readonly"`
}

// PortMapping maps a host port to a container port.
type PortMapping struct {
	Host      int    `json:"host"`
	Container int    `json:"container"`
	Protocol  string `json:"protocol"`
}

// ContainerExecution runs a script inside a Docker container.
type ContainerExecution struct {
	Script     string            `json:"script"`
	BaseImage  string            `json:"base_image"`
	Blocks     []DockerfileBlock `json:"blocks,omitempty"`
	Env        []KeyValue        `json:"env,omitempty"`
	Volumes    []VolumeMount     `json:"volumes,omitempty"`
	Ports      []PortMapping     `json:"ports,omitempty"`
	Dockerfile string            `json:"dockerfile,omitempty"`
}

func (e *ContainerExecution) ExecType() string { return "container" }

// BuildDockerfile generates a Dockerfile from the execution definition.
// If a raw Dockerfile is provided, it is returned as-is.
// Otherwise, one is assembled from the base image and enabled blocks.
func (e *ContainerExecution) BuildDockerfile() string {
	if e.Dockerfile != "" {
		return e.Dockerfile
	}

	var b strings.Builder
	b.WriteString("FROM ")
	b.WriteString(e.BaseImage)
	b.WriteByte('\n')

	for _, block := range e.Blocks {
		if !block.Enabled || block.Dockerfile == "" {
			continue
		}
		b.WriteString("\n# ")
		b.WriteString(block.Label)
		b.WriteByte('\n')
		b.WriteString(block.Dockerfile)
		b.WriteByte('\n')
	}

	b.WriteString("\nCOPY script.sh /runwisp-script.sh\n")
	b.WriteString("RUN chmod +x /runwisp-script.sh\n")
	b.WriteString("ENTRYPOINT [\"/bin/sh\", \"/runwisp-script.sh\"]\n")

	return b.String()
}

// --- HTTP execution ---

// HTTPBody describes the request body for HTTP executions.
type HTTPBody struct {
	Kind   string     `json:"kind"`
	JSON   string     `json:"json,omitempty"`
	Fields []KeyValue `json:"fields,omitempty"`
}

// HTTPExecution sends an HTTP request and treats the response as output.
type HTTPExecution struct {
	Method  string     `json:"method"`
	URL     string     `json:"url"`
	Headers []KeyValue `json:"headers,omitempty"`
	Body    *HTTPBody  `json:"body,omitempty"`
}

func (e *HTTPExecution) ExecType() string { return "http" }

// --- Config execution (resolved server-side; the daemon should not receive this in dispatch) ---

// ConfigExecution references a daemon-local task by name.
type ConfigExecution struct {
	TaskName string `json:"task_name"`
}

func (e *ConfigExecution) ExecType() string { return "config" }

// --- Compose execution ---

// ComposeMode discriminates per-service runs from whole-stack runs.
const (
	ComposeModeServices = "services"
	ComposeModeStack    = "stack"
)

// ComposePull controls the --pull flag passed to docker compose run.
const (
	ComposePullMissing = "missing"
	ComposePullAlways  = "always"
	ComposePullNever   = "never"
)

// ComposeExecution runs a service (or whole project) defined in a docker
// compose file. The backend shells out to `docker compose`; the daemon never
// parses the compose file at runtime — enumeration happens at config load.
type ComposeExecution struct {
	File        string   `json:"file"`
	ProjectName string   `json:"project_name"`
	Service     string   `json:"service,omitempty"` // empty when Mode == stack
	Mode        string   `json:"mode"`              // "services" | "stack"
	Profiles    []string `json:"profiles,omitempty"`
	EnvFile     []string `json:"env_file,omitempty"`
	WorkingDir  string   `json:"working_dir,omitempty"`
	WithDeps    bool     `json:"with_deps,omitempty"`
	Pull        string   `json:"pull,omitempty"` // "missing" | "always" | "never"
}

func (e *ComposeExecution) ExecType() string { return "compose" }

// --- JSON parsing ---

var executionFactories = map[string]func() ExecutionDef{
	"shell":     func() ExecutionDef { return &ShellExecution{} },
	"container": func() ExecutionDef { return &ContainerExecution{} },
	"http":      func() ExecutionDef { return &HTTPExecution{} },
	"config":    func() ExecutionDef { return &ConfigExecution{} },
	"compose":   func() ExecutionDef { return &ComposeExecution{} },
}

func ParseExecutionDef(data json.RawMessage) (ExecutionDef, error) {
	if len(data) == 0 || string(data) == "null" {
		return nil, fmt.Errorf("empty execution definition")
	}

	var envelope struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("failed to read execution type: %w", err)
	}

	factory, ok := executionFactories[envelope.Type]
	if !ok {
		return nil, fmt.Errorf("unsupported execution type: %q", envelope.Type)
	}

	def := factory()
	if err := json.Unmarshal(data, def); err != nil {
		return nil, fmt.Errorf("invalid %s execution: %w", envelope.Type, err)
	}
	return def, nil
}

// MarshalExecutionDef serializes an ExecutionDef to JSON, including the type discriminator.
func MarshalExecutionDef(def ExecutionDef) (json.RawMessage, error) {
	raw, err := json.Marshal(def)
	if err != nil {
		return nil, err
	}
	if len(raw) < 2 {
		return json.Marshal(map[string]string{"type": def.ExecType()})
	}
	prefix := fmt.Sprintf(`{"type":%q,`, def.ExecType())
	return append([]byte(prefix), raw[1:]...), nil
}
