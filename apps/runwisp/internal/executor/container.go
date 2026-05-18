// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package executor

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/docker/go-connections/nat"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/runwisp/runwisp/internal/model"
	"log/slog"
)

// allowedVolumePrefixes lists host path prefixes that may be bind-mounted.
// Only /tmp and the current working directory are allowed by default.
var allowedVolumePrefixes = []string{
	"/tmp",
}

// validateVolumeMount rejects host paths that are not under an allowed prefix.
// Symlinks are resolved to prevent bypass via indirection.
func validateVolumeMount(hostPath string) error {
	clean := filepath.Clean(hostPath)

	// Resolve symlinks to get the real path on the host.
	resolved, err := filepath.EvalSymlinks(clean)
	if err != nil {
		// If the path doesn't exist yet, use the cleaned path.
		resolved = clean
	}

	cwd, _ := os.Getwd()
	allowed := append(allowedVolumePrefixes, cwd)

	for _, prefix := range allowed {
		if resolved == prefix || strings.HasPrefix(resolved, prefix+"/") {
			return nil
		}
	}
	return fmt.Errorf("mounting %q is not allowed; only paths under /tmp or the working directory are permitted", hostPath)
}

// dockerClient abstracts the Docker SDK methods used by ContainerBackend for testability.
type dockerClient interface {
	Ping(ctx context.Context) (types.Ping, error)
	ImageBuild(ctx context.Context, buildContext io.Reader, options build.ImageBuildOptions) (build.ImageBuildResponse, error)
	ContainerCreate(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error)
	ContainerAttach(ctx context.Context, container string, options container.AttachOptions) (types.HijackedResponse, error)
	ContainerStart(ctx context.Context, container string, options container.StartOptions) error
	ContainerWait(ctx context.Context, container string, condition container.WaitCondition) (<-chan container.WaitResponse, <-chan error)
	ContainerRemove(ctx context.Context, container string, options container.RemoveOptions) error
	ImageRemove(ctx context.Context, imageRef string, options image.RemoveOptions) ([]image.DeleteResponse, error)
}

// ContainerBackend executes tasks inside Docker containers using the Docker SDK.
type ContainerBackend struct {
	docker  dockerClient
	builder *ImageBuilder
}

// NewContainerBackend tries the environment-configured host first (DOCKER_HOST),
// then falls back to well-known socket paths including the Podman Docker-compatible socket.
func NewContainerBackend(ctx context.Context) (*ContainerBackend, error) {
	// If DOCKER_HOST is set, try only that.
	if os.Getenv("DOCKER_HOST") != "" {
		return tryDockerClient(ctx)
	}

	// Try the default Docker socket first.
	if cli, err := tryDockerClient(ctx); err == nil {
		return cli, nil
	}

	// Fall back to well-known Podman socket paths.
	uid := os.Getuid()
	candidates := []string{
		fmt.Sprintf("/run/user/%d/podman/podman.sock", uid), // rootless Podman
		"/run/podman/podman.sock",                           // rootful Podman
	}

	var lastErr error
	for _, sock := range candidates {
		if _, err := os.Stat(sock); err != nil {
			continue
		}
		host := "unix://" + sock
		cli, err := client.NewClientWithOpts(
			client.WithHost(host),
			client.WithAPIVersionNegotiation(),
		)
		if err != nil {
			lastErr = fmt.Errorf("create client for %s: %w", sock, err)
			continue
		}
		if _, err := cli.Ping(ctx); err != nil {
			cli.Close()
			lastErr = fmt.Errorf("daemon at %s unreachable: %w", sock, err)
			continue
		}
		return &ContainerBackend{docker: cli, builder: &ImageBuilder{docker: cli}}, nil
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("no Docker or Podman daemon found; install Docker or enable the Podman socket (systemctl --user enable --now podman.socket)")
}

// tryDockerClient attempts to connect using the default Docker client options.
func tryDockerClient(ctx context.Context) (*ContainerBackend, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("create docker client: %w", err)
	}

	if _, err := cli.Ping(ctx); err != nil {
		cli.Close()
		return nil, fmt.Errorf("docker daemon unreachable: %w", err)
	}

	return &ContainerBackend{docker: cli, builder: &ImageBuilder{docker: cli}}, nil
}

// NewContainerBackendFromClient creates a ContainerBackend from an existing Docker client.
// Primarily useful in tests.
func NewContainerBackendFromClient(docker dockerClient) *ContainerBackend {
	return &ContainerBackend{docker: docker, builder: &ImageBuilder{docker: docker}}
}

// Available reports whether the Docker daemon is reachable.
func (b *ContainerBackend) Available(ctx context.Context) bool {
	if b == nil || b.docker == nil {
		return false
	}
	_, err := b.docker.Ping(ctx)
	return err == nil
}

func (b *ContainerBackend) Start(ctx context.Context, task *model.Task, def model.ExecutionDef) (*Process, error) {
	ctr, ok := def.(*model.ContainerExecution)
	if !ok {
		return nil, fmt.Errorf("ContainerBackend received non-container execution: %s", def.ExecType())
	}

	imageTag, err := b.builder.Build(ctx, ctr)
	if err != nil {
		return nil, err
	}

	for _, vol := range ctr.Volumes {
		if err := validateVolumeMount(vol.Host); err != nil {
			return nil, fmt.Errorf("container volume mount rejected: %w", err)
		}
	}

	// Configure the container
	containerConfig, hostConfig := b.buildContainerConfig(imageTag, ctr, task)

	// Create the container
	created, err := b.docker.ContainerCreate(ctx, containerConfig, hostConfig, nil, nil, "")
	if err != nil {
		b.builder.Remove(ctx, imageTag)
		return nil, fmt.Errorf("container create: %w", err)
	}

	containerID := created.ID

	// Attach to get stdout/stderr before starting
	attachResp, err := b.docker.ContainerAttach(ctx, containerID, container.AttachOptions{
		Stream: true,
		Stdout: true,
		Stderr: true,
	})
	if err != nil {
		b.removeContainer(ctx, containerID)
		b.builder.Remove(ctx, imageTag)
		return nil, fmt.Errorf("container attach: %w", err)
	}

	// Start the container
	if err := b.docker.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		attachResp.Close()
		b.removeContainer(ctx, containerID)
		b.builder.Remove(ctx, imageTag)
		return nil, fmt.Errorf("container start: %w", err)
	}

	// Demultiplex the Docker attach stream into separate stdout/stderr readers.
	// Without a TTY, Docker frames each chunk with an 8-byte header (stream type + size).
	stdoutPR, stdoutPW := io.Pipe()
	stderrPR, stderrPW := io.Pipe()
	go func() {
		_, err := stdcopy.StdCopy(stdoutPW, stderrPW, attachResp.Reader)
		stdoutPW.CloseWithError(err)
		stderrPW.CloseWithError(err)
	}()

	return &Process{
		Stdout: stdoutPR,
		Stderr: stderrPR,
		Wait: func() (int, error) {
			statusCh, errCh := b.docker.ContainerWait(ctx, containerID, container.WaitConditionNotRunning)
			for {
				select {
				case err := <-errCh:
					if err != nil {
						return -1, err
					}
					errCh = nil // already drained, ignore further reads
				case status := <-statusCh:
					return int(status.StatusCode), nil
				}
			}
		},
		Cleanup: func() {
			attachResp.Close()
			b.removeContainer(context.Background(), containerID)
			b.builder.Remove(context.Background(), imageTag)
		},
	}, nil
}

func (b *ContainerBackend) removeContainer(ctx context.Context, containerID string) {
	if err := b.docker.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true}); err != nil {
		slog.Warn("Failed to remove container", "id", containerID, "err", err)
	}
}

func (b *ContainerBackend) buildContainerConfig(imageTag string, ctr *model.ContainerExecution, task *model.Task) (*container.Config, *container.HostConfig) {
	baseEnv := make([]string, 0, len(ctr.Env))
	for _, kv := range ctr.Env {
		baseEnv = append(baseEnv, kv.Key+"="+kv.Value)
	}
	// Overlay Task.Env then Task.SecretEnv on top of the container-execution
	// env so task-level entries (defined alongside cron/run) win over
	// container-specific defaults, matching the shell backend's precedence.
	var env []string
	if task != nil && (len(task.Env) > 0 || len(task.SecretEnv) > 0) {
		env = buildProcessEnv(baseEnv, task.Env, task.SecretEnv)
	} else {
		env = baseEnv
	}

	exposedPorts := nat.PortSet{}
	portBindings := nat.PortMap{}
	for _, p := range ctr.Ports {
		proto := strings.ToLower(p.Protocol)
		if proto == "" {
			proto = "tcp"
		}
		containerPort := nat.Port(strconv.Itoa(p.Container) + "/" + proto)
		exposedPorts[containerPort] = struct{}{}
		portBindings[containerPort] = []nat.PortBinding{
			{HostPort: strconv.Itoa(p.Host)},
		}
	}

	mounts := make([]mount.Mount, 0, len(ctr.Volumes))
	for _, vol := range ctr.Volumes {
		mounts = append(mounts, mount.Mount{
			Type:     mount.TypeBind,
			Source:   vol.Host,
			Target:   vol.Container,
			ReadOnly: vol.ReadOnly,
		})
	}

	containerCfg := &container.Config{
		Image:        imageTag,
		Env:          env,
		ExposedPorts: exposedPorts,
	}

	hostCfg := &container.HostConfig{
		PortBindings: portBindings,
		Mounts:       mounts,
		Privileged:   false,
		SecurityOpt:  []string{"no-new-privileges:true"},
		CapDrop:      []string{"SYS_ADMIN", "SYS_PTRACE", "SYS_RAWIO", "SYS_MODULE", "NET_ADMIN", "DAC_OVERRIDE", "MKNOD", "AUDIT_WRITE", "SETFCAP"},
	}

	return containerCfg, hostCfg
}
