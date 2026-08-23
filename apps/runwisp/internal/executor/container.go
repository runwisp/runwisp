// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"log/slog"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	"github.com/runwisp/runwisp/internal/model"
)

// allowedVolumePrefixes lists host path prefixes that may be bind-mounted.
// Only /tmp and the current working directory are allowed by default.
var allowedVolumePrefixes = []string{
	"/tmp",
}

// containerCleanupTimeout bounds the ContainerRemove/ImageRemove calls made
// from Process.Cleanup. Cleanup runs in the same goroutine the daemon shutdown
// coordinator waits on after ForceKill has already unblocked it; without a
// deadline a hung (not merely unreachable — that fails fast) Docker/Podman
// engine would stall shutdown indefinitely even though the process itself is
// already dead. A var (not const) so tests can shrink it instead of waiting
// out the real 10s to prove the deadline fires.
var containerCleanupTimeout = 10 * time.Second

// validateVolumeMount rejects host paths that are not under an allowed prefix.
// Symlinks are resolved to prevent bypass via indirection.
func validateVolumeMount(hostPath string) error {
	clean := filepath.Clean(hostPath)

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
	Ping(ctx context.Context, options client.PingOptions) (client.PingResult, error)
	ImageBuild(ctx context.Context, buildContext io.Reader, options client.ImageBuildOptions) (client.ImageBuildResult, error)
	ContainerCreate(ctx context.Context, options client.ContainerCreateOptions) (client.ContainerCreateResult, error)
	ContainerAttach(ctx context.Context, containerID string, options client.ContainerAttachOptions) (client.ContainerAttachResult, error)
	ContainerStart(ctx context.Context, containerID string, options client.ContainerStartOptions) (client.ContainerStartResult, error)
	ContainerWait(ctx context.Context, containerID string, options client.ContainerWaitOptions) client.ContainerWaitResult
	ContainerStop(ctx context.Context, containerID string, options client.ContainerStopOptions) (client.ContainerStopResult, error)
	ContainerKill(ctx context.Context, containerID string, options client.ContainerKillOptions) (client.ContainerKillResult, error)
	ContainerRemove(ctx context.Context, containerID string, options client.ContainerRemoveOptions) (client.ContainerRemoveResult, error)
	ImageRemove(ctx context.Context, imageRef string, options client.ImageRemoveOptions) (client.ImageRemoveResult, error)
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
		if _, err := cli.Ping(ctx, client.PingOptions{}); err != nil {
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

	if _, err := cli.Ping(ctx, client.PingOptions{}); err != nil {
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
	_, err := b.docker.Ping(ctx, client.PingOptions{})
	return err == nil
}

func (b *ContainerBackend) Start(ctx context.Context, task *model.Task, run *model.Run, def model.ExecutionDef) (*Process, error) {
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

	containerID, attachResp, err := b.createAndStartContainer(ctx, imageTag, ctr, task, run)
	if err != nil {
		return nil, err
	}

	stdoutPR, stderrPR := demuxAttachStream(attachResp)

	// The container's lifetime is decoupled from the run ctx so a stop/timeout
	// runs the signal ladder instead of an instant force-remove: ContainerWait
	// blocks on a cancel-decoupled context derived from ctx via
	// context.WithoutCancel (it keeps ctx's values but is never cancelled, so the
	// wait returns only when the container truly exits), while a watcher
	// translates run-ctx cancellation into a graceful ContainerStop (stop_signal,
	// then SIGKILL after graceful_stop). Mirrors the shell/compose cmd.Cancel
	// ladder, using Docker's native stop semantics.
	waitFn := b.waitFunc(context.WithoutCancel(ctx), containerID)
	done := make(chan struct{})
	var doneOnce sync.Once
	closeDone := func() { doneOnce.Do(func() { close(done) }) }

	go func() {
		select {
		case <-ctx.Done():
			b.gracefulStopContainer(containerID, task)
		case <-done:
		}
	}()

	return &Process{
		Stdout: stdoutPR,
		Stderr: stderrPR,
		Wait: func() (int, error) {
			code, waitErr := waitFn()
			closeDone()
			return code, waitErr
		},
		// ForceKill skips the graceful window (daemon shutdown fast path):
		// SIGKILL the container now rather than sending stop_signal first.
		ForceKill: func() {
			if _, err := b.docker.ContainerKill(context.Background(), containerID, client.ContainerKillOptions{Signal: "SIGKILL"}); err != nil {
				slog.Warn("Failed to kill container", "id", containerID, "err", err)
			}
		},
		Cleanup: func() {
			closeDone()
			attachResp.Close()
			cleanupCtx, cancel := context.WithTimeout(context.Background(), containerCleanupTimeout)
			defer cancel()
			b.removeContainer(cleanupCtx, containerID)
			b.builder.Remove(cleanupCtx, imageTag)
		},
	}, nil
}

// gracefulStopContainer sends the task's stop_signal to the container and lets
// Docker force-kill it after the graceful_stop window (its native ladder:
// signal, wait Timeout seconds, then SIGKILL). A non-positive graceful_stop maps
// to Timeout=0 — no grace, immediate kill — matching the shell ladder's
// "grace <= 0 goes straight to SIGKILL".
func (b *ContainerBackend) gracefulStopContainer(containerID string, task *model.Task) {
	opts := client.ContainerStopOptions{}
	if sig, ok := model.NormalizeSignalName(task.StopSignal); ok {
		opts.Signal = sig
	}
	secs := gracefulStopSeconds(task.GracefulStop)
	opts.Timeout = &secs
	if _, err := b.docker.ContainerStop(context.Background(), containerID, opts); err != nil {
		slog.Warn("Failed to stop container gracefully", "id", containerID, "err", err)
	}
}

// gracefulStopSeconds converts a graceful_stop Duration to Docker's whole-second
// stop timeout, rounding up so a sub-second window still grants one second of
// grace rather than collapsing to an immediate kill.
func gracefulStopSeconds(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	return int((d + time.Second - 1) / time.Second)
}

// createAndStartContainer configures, creates, attaches to, and starts the
// container. On any failure it tears down whatever was created (container,
// image) so callers see a clean error and no leaked resources. It returns the
// container ID and the live attach response on success.
func (b *ContainerBackend) createAndStartContainer(ctx context.Context, imageTag string, ctr *model.ContainerExecution, task *model.Task, run *model.Run) (string, client.ContainerAttachResult, error) {
	containerConfig, hostConfig := b.buildContainerConfig(imageTag, ctr, task, run)

	// Cleanup must run on a context detached from ctx: a start that fails
	// *because* ctx was cancelled (timeout, manager stop) would otherwise pass
	// the already-cancelled ctx to ContainerRemove/ImageRemove, which return
	// immediately and leak the container and image. WithoutCancel keeps ctx's
	// values but is never cancelled itself.
	cleanupCtx := context.WithoutCancel(ctx)

	created, err := b.docker.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config:     containerConfig,
		HostConfig: hostConfig,
	})
	if err != nil {
		b.builder.Remove(cleanupCtx, imageTag)
		return "", client.ContainerAttachResult{}, fmt.Errorf("container create: %w", err)
	}

	containerID := created.ID

	// Attach to get stdout/stderr before starting.
	attachResp, err := b.docker.ContainerAttach(ctx, containerID, client.ContainerAttachOptions{
		Stream: true,
		Stdout: true,
		Stderr: true,
	})
	if err != nil {
		b.removeContainer(cleanupCtx, containerID)
		b.builder.Remove(cleanupCtx, imageTag)
		return "", client.ContainerAttachResult{}, fmt.Errorf("container attach: %w", err)
	}

	if _, err := b.docker.ContainerStart(ctx, containerID, client.ContainerStartOptions{}); err != nil {
		attachResp.Close()
		b.removeContainer(cleanupCtx, containerID)
		b.builder.Remove(cleanupCtx, imageTag)
		return "", client.ContainerAttachResult{}, fmt.Errorf("container start: %w", err)
	}

	return containerID, attachResp, nil
}

// demuxAttachStream demultiplexes the Docker attach stream into separate
// stdout/stderr readers. Without a TTY, Docker frames each chunk with an 8-byte
// header (stream type + size).
func demuxAttachStream(attachResp client.ContainerAttachResult) (io.ReadCloser, io.ReadCloser) {
	stdoutPR, stdoutPW := io.Pipe()
	stderrPR, stderrPW := io.Pipe()
	go func() {
		_, err := stdcopy.StdCopy(stdoutPW, stderrPW, attachResp.Reader)
		stdoutPW.CloseWithError(err)
		stderrPW.CloseWithError(err)
	}()
	return stdoutPR, stderrPR
}

// waitFunc returns the Process.Wait callback that blocks on container exit and
// reports its status code.
func (b *ContainerBackend) waitFunc(ctx context.Context, containerID string) func() (int, error) {
	return func() (int, error) {
		waitRes := b.docker.ContainerWait(ctx, containerID, client.ContainerWaitOptions{Condition: container.WaitConditionNotRunning})
		statusCh, errCh := waitRes.Result, waitRes.Error
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
	}
}

func (b *ContainerBackend) removeContainer(ctx context.Context, containerID string) {
	if _, err := b.docker.ContainerRemove(ctx, containerID, client.ContainerRemoveOptions{Force: true}); err != nil {
		slog.Warn("Failed to remove container", "id", containerID, "err", err)
	}
}

func (b *ContainerBackend) buildContainerConfig(imageTag string, ctr *model.ContainerExecution, task *model.Task, run *model.Run) (*container.Config, *container.HostConfig) {
	ctrEnv := make(map[string]string, len(ctr.Env))
	for _, kv := range ctr.Env {
		ctrEnv[kv.Key] = kv.Value
	}
	// Overlay Task.Env then Task.Secrets then any env-kind per-run params on top
	// of the container-execution env so task-level entries (defined alongside
	// cron/run) win over container-specific defaults, matching the shell
	// backend's precedence. arg/option/flag params have no seam here (the image
	// entrypoint is a fixed script, not an argv we control), so only env params
	// reach a container run — container/HTTP backends aren't built from
	// [tasks.*] TOML today, so this is forward-safety, not a reachable path.
	//
	// buildProcessEnv's first argument is meant to be the daemon's own OS
	// environment, whose RUNWISP_-prefixed secrets it strips before a task
	// inherits it. ctr.Env is a container execution's own declared vars, not
	// the daemon's process env, so it must never go through that filter — it
	// layers in as an ordinary map instead, same as the other layers.
	var paramEnv map[string]string
	var taskEnv, taskSecrets map[string]string
	if task != nil {
		taskEnv = task.Env
		taskSecrets = task.Secrets
		if run != nil {
			paramEnv = model.ParamEnvLayer(task.Parameters, run.Params)
		}
	}
	env := buildProcessEnv(nil, ctrEnv, taskEnv, taskSecrets, paramEnv)

	exposedPorts := network.PortSet{}
	portBindings := network.PortMap{}
	for _, p := range ctr.Ports {
		proto := strings.ToLower(p.Protocol)
		if proto == "" {
			proto = "tcp"
		}
		containerPort := network.MustParsePort(strconv.Itoa(p.Container) + "/" + proto)
		exposedPorts[containerPort] = struct{}{}
		portBindings[containerPort] = []network.PortBinding{
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
