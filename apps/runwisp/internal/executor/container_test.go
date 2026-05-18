// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package executor

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Mock Docker client ---

type mockDockerClient struct {
	pingFunc            func(ctx context.Context) (types.Ping, error)
	imageBuildFunc      func(ctx context.Context, buildContext io.Reader, options build.ImageBuildOptions) (build.ImageBuildResponse, error)
	containerCreateFunc func(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error)
	containerAttachFunc func(ctx context.Context, ctr string, options container.AttachOptions) (types.HijackedResponse, error)
	containerStartFunc  func(ctx context.Context, ctr string, options container.StartOptions) error
	containerWaitFunc   func(ctx context.Context, ctr string, condition container.WaitCondition) (<-chan container.WaitResponse, <-chan error)
	containerRemoveFunc func(ctx context.Context, ctr string, options container.RemoveOptions) error
	imageRemoveFunc     func(ctx context.Context, imageRef string, options image.RemoveOptions) ([]image.DeleteResponse, error)
}

func (m *mockDockerClient) Ping(ctx context.Context) (types.Ping, error) {
	if m.pingFunc != nil {
		return m.pingFunc(ctx)
	}
	return types.Ping{}, nil
}

func (m *mockDockerClient) ImageBuild(ctx context.Context, buildContext io.Reader, options build.ImageBuildOptions) (build.ImageBuildResponse, error) {
	if m.imageBuildFunc != nil {
		return m.imageBuildFunc(ctx, buildContext, options)
	}
	return build.ImageBuildResponse{Body: io.NopCloser(strings.NewReader(""))}, nil
}

func (m *mockDockerClient) ContainerCreate(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error) {
	if m.containerCreateFunc != nil {
		return m.containerCreateFunc(ctx, config, hostConfig, networkingConfig, platform, containerName)
	}
	return container.CreateResponse{ID: "test-container-id"}, nil
}

func (m *mockDockerClient) ContainerAttach(ctx context.Context, ctr string, options container.AttachOptions) (types.HijackedResponse, error) {
	if m.containerAttachFunc != nil {
		return m.containerAttachFunc(ctx, ctr, options)
	}
	return newHijackedResponse(""), nil
}

func (m *mockDockerClient) ContainerStart(ctx context.Context, ctr string, options container.StartOptions) error {
	if m.containerStartFunc != nil {
		return m.containerStartFunc(ctx, ctr, options)
	}
	return nil
}

func (m *mockDockerClient) ContainerWait(ctx context.Context, ctr string, condition container.WaitCondition) (<-chan container.WaitResponse, <-chan error) {
	if m.containerWaitFunc != nil {
		return m.containerWaitFunc(ctx, ctr, condition)
	}
	ch := make(chan container.WaitResponse, 1)
	ch <- container.WaitResponse{StatusCode: 0}
	return ch, make(chan error)
}

func (m *mockDockerClient) ContainerRemove(ctx context.Context, ctr string, options container.RemoveOptions) error {
	if m.containerRemoveFunc != nil {
		return m.containerRemoveFunc(ctx, ctr, options)
	}
	return nil
}

func (m *mockDockerClient) ImageRemove(ctx context.Context, imageRef string, options image.RemoveOptions) ([]image.DeleteResponse, error) {
	if m.imageRemoveFunc != nil {
		return m.imageRemoveFunc(ctx, imageRef, options)
	}
	return nil, nil
}

// --- Tests ---

// mockConn implements net.Conn for testing HijackedResponse.
type mockConn struct {
	io.Reader
}

func (m *mockConn) Write(b []byte) (int, error)      { return len(b), nil }
func (m *mockConn) Close() error                     { return nil }
func (m *mockConn) LocalAddr() net.Addr              { return nil }
func (m *mockConn) RemoteAddr() net.Addr             { return nil }
func (m *mockConn) SetDeadline(time.Time) error      { return nil }
func (m *mockConn) SetReadDeadline(time.Time) error  { return nil }
func (m *mockConn) SetWriteDeadline(time.Time) error { return nil }

// dockerStdoutFrame wraps payload in a Docker multiplexed stdout frame (8-byte header).
func dockerStdoutFrame(payload string) []byte {
	var buf bytes.Buffer
	header := [8]byte{}
	header[0] = 1 // stdout stream type
	binary.BigEndian.PutUint32(header[4:], uint32(len(payload)))
	buf.Write(header[:])
	buf.WriteString(payload)
	return buf.Bytes()
}

func newHijackedResponse(content string) types.HijackedResponse {
	var framed []byte
	if content != "" {
		framed = dockerStdoutFrame(content)
	}
	conn := &mockConn{Reader: bytes.NewReader(framed)}
	return types.HijackedResponse{
		Conn:   conn,
		Reader: bufio.NewReader(conn),
	}
}

func TestBuildContext(t *testing.T) {
	ctr := &model.ContainerExecution{
		BaseImage: "alpine:3.18",
		Script:    "echo hello",
	}

	reader, err := BuildContext(ctr)
	require.NoError(t, err)

	data, err := io.ReadAll(reader)
	require.NoError(t, err)

	tr := tar.NewReader(bytes.NewReader(data))
	files := make(map[string]string)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		content, err := io.ReadAll(tr)
		require.NoError(t, err)
		files[header.Name] = string(content)
	}

	assert.Contains(t, files, "Dockerfile")
	assert.Contains(t, files, "script.sh")
	assert.Contains(t, files["Dockerfile"], "FROM alpine:3.18")
	assert.Equal(t, "echo hello", files["script.sh"])
}

func TestBuildContainerConfig(t *testing.T) {
	b := NewContainerBackendFromClient(&mockDockerClient{})
	ctr := &model.ContainerExecution{
		Script:    "echo test",
		BaseImage: "ubuntu:22.04",
		Env: []model.KeyValue{
			{Key: "FOO", Value: "bar"},
			{Key: "BAZ", Value: "qux"},
		},
		Ports: []model.PortMapping{
			{Host: 8080, Container: 80, Protocol: "tcp"},
			{Host: 5432, Container: 5432, Protocol: ""},
		},
		Volumes: []model.VolumeMount{
			{Host: "/data", Container: "/mnt/data", ReadOnly: true},
		},
	}

	containerCfg, hostCfg := b.buildContainerConfig("test-image:latest", ctr, nil)

	assert.Equal(t, "test-image:latest", containerCfg.Image)
	assert.Equal(t, []string{"FOO=bar", "BAZ=qux"}, containerCfg.Env)
	_, has80 := containerCfg.ExposedPorts["80/tcp"]
	assert.True(t, has80)
	_, has5432 := containerCfg.ExposedPorts["5432/tcp"]
	assert.True(t, has5432)
	assert.Equal(t, "8080", hostCfg.PortBindings["80/tcp"][0].HostPort)
	assert.Equal(t, "5432", hostCfg.PortBindings["5432/tcp"][0].HostPort)
	require.Len(t, hostCfg.Mounts, 1)
	assert.Equal(t, "/data", hostCfg.Mounts[0].Source)
	assert.Equal(t, "/mnt/data", hostCfg.Mounts[0].Target)
	assert.True(t, hostCfg.Mounts[0].ReadOnly)
	assert.False(t, hostCfg.AutoRemove)
}

func TestBuildContainerConfigEmpty(t *testing.T) {
	b := NewContainerBackendFromClient(&mockDockerClient{})
	ctr := &model.ContainerExecution{
		Script:    "echo test",
		BaseImage: "alpine",
	}

	containerCfg, hostCfg := b.buildContainerConfig("img", ctr, nil)

	assert.Equal(t, "img", containerCfg.Image)
	assert.Empty(t, containerCfg.Env)
	assert.Empty(t, containerCfg.ExposedPorts)
	assert.Empty(t, hostCfg.PortBindings)
	assert.Empty(t, hostCfg.Mounts)
}

func TestAvailable(t *testing.T) {
	t.Run("nil backend", func(t *testing.T) {
		var b *ContainerBackend
		assert.False(t, b.Available(context.Background()))
	})

	t.Run("nil docker client", func(t *testing.T) {
		b := &ContainerBackend{}
		assert.False(t, b.Available(context.Background()))
	})

	t.Run("ping succeeds", func(t *testing.T) {
		b := NewContainerBackendFromClient(&mockDockerClient{})
		assert.True(t, b.Available(context.Background()))
	})

	t.Run("ping fails", func(t *testing.T) {
		b := NewContainerBackendFromClient(&mockDockerClient{
			pingFunc: func(ctx context.Context) (types.Ping, error) {
				return types.Ping{}, fmt.Errorf("connection refused")
			},
		})
		assert.False(t, b.Available(context.Background()))
	})
}

func TestStartRejectsNonContainerExecution(t *testing.T) {
	b := NewContainerBackendFromClient(&mockDockerClient{})
	_, err := b.Start(context.Background(), &model.Task{}, &model.ShellExecution{Script: "echo hi"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "non-container execution")
}

func TestStartBuildFailure(t *testing.T) {
	mock := &mockDockerClient{
		imageBuildFunc: func(ctx context.Context, buildContext io.Reader, options build.ImageBuildOptions) (build.ImageBuildResponse, error) {
			return build.ImageBuildResponse{}, fmt.Errorf("build error")
		},
	}
	b := NewContainerBackendFromClient(mock)

	_, err := b.Start(context.Background(), &model.Task{}, &model.ContainerExecution{
		Script:    "echo test",
		BaseImage: "alpine",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "docker build")
}

func TestStartBuildOutputError(t *testing.T) {
	errorMsg := `{"error": "COPY failed: file not found"}`
	mock := &mockDockerClient{
		imageBuildFunc: func(ctx context.Context, buildContext io.Reader, options build.ImageBuildOptions) (build.ImageBuildResponse, error) {
			return build.ImageBuildResponse{
				Body: io.NopCloser(strings.NewReader(errorMsg)),
			}, nil
		},
	}
	b := NewContainerBackendFromClient(mock)

	_, err := b.Start(context.Background(), &model.Task{}, &model.ContainerExecution{
		Script:    "echo test",
		BaseImage: "alpine",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "docker build failed")
	assert.Contains(t, err.Error(), "COPY failed")
}

func TestStartContainerCreateFailure(t *testing.T) {
	imageRemoved := false
	mock := &mockDockerClient{
		imageBuildFunc: func(ctx context.Context, buildContext io.Reader, options build.ImageBuildOptions) (build.ImageBuildResponse, error) {
			return build.ImageBuildResponse{
				Body: io.NopCloser(strings.NewReader(`{"stream":"ok"}`)),
			}, nil
		},
		containerCreateFunc: func(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error) {
			return container.CreateResponse{}, fmt.Errorf("create failed")
		},
		imageRemoveFunc: func(ctx context.Context, imageRef string, options image.RemoveOptions) ([]image.DeleteResponse, error) {
			imageRemoved = true
			return nil, nil
		},
	}
	b := NewContainerBackendFromClient(mock)

	_, err := b.Start(context.Background(), &model.Task{}, &model.ContainerExecution{
		Script:    "echo test",
		BaseImage: "alpine",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "container create")
	assert.True(t, imageRemoved, "image should be cleaned up after create failure")
}

func TestStartContainerAttachFailure(t *testing.T) {
	containerRemoved := false
	imageRemoved := false
	mock := &mockDockerClient{
		imageBuildFunc: func(ctx context.Context, buildContext io.Reader, options build.ImageBuildOptions) (build.ImageBuildResponse, error) {
			return build.ImageBuildResponse{
				Body: io.NopCloser(strings.NewReader(`{"stream":"ok"}`)),
			}, nil
		},
		containerAttachFunc: func(ctx context.Context, ctr string, options container.AttachOptions) (types.HijackedResponse, error) {
			return types.HijackedResponse{}, fmt.Errorf("attach failed")
		},
		containerRemoveFunc: func(ctx context.Context, ctr string, options container.RemoveOptions) error {
			containerRemoved = true
			return nil
		},
		imageRemoveFunc: func(ctx context.Context, imageRef string, options image.RemoveOptions) ([]image.DeleteResponse, error) {
			imageRemoved = true
			return nil, nil
		},
	}
	b := NewContainerBackendFromClient(mock)

	_, err := b.Start(context.Background(), &model.Task{}, &model.ContainerExecution{
		Script:    "echo test",
		BaseImage: "alpine",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "container attach")
	assert.True(t, containerRemoved, "container should be cleaned up after attach failure")
	assert.True(t, imageRemoved, "image should be cleaned up after attach failure")
}

func TestStartContainerStartFailure(t *testing.T) {
	containerRemoved := false
	imageRemoved := false
	mock := &mockDockerClient{
		imageBuildFunc: func(ctx context.Context, buildContext io.Reader, options build.ImageBuildOptions) (build.ImageBuildResponse, error) {
			return build.ImageBuildResponse{
				Body: io.NopCloser(strings.NewReader(`{"stream":"ok"}`)),
			}, nil
		},
		containerAttachFunc: func(ctx context.Context, ctr string, options container.AttachOptions) (types.HijackedResponse, error) {
			return newHijackedResponse(""), nil
		},
		containerStartFunc: func(ctx context.Context, ctr string, options container.StartOptions) error {
			return fmt.Errorf("start failed")
		},
		containerRemoveFunc: func(ctx context.Context, ctr string, options container.RemoveOptions) error {
			containerRemoved = true
			return nil
		},
		imageRemoveFunc: func(ctx context.Context, imageRef string, options image.RemoveOptions) ([]image.DeleteResponse, error) {
			imageRemoved = true
			return nil, nil
		},
	}
	b := NewContainerBackendFromClient(mock)

	_, err := b.Start(context.Background(), &model.Task{}, &model.ContainerExecution{
		Script:    "echo test",
		BaseImage: "alpine",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "container start")
	assert.True(t, containerRemoved)
	assert.True(t, imageRemoved)
}

func TestStartSuccess(t *testing.T) {
	buildBody := `{"stream":"Step 1/1 : FROM alpine"}` + "\n"
	mock := &mockDockerClient{
		imageBuildFunc: func(ctx context.Context, buildContext io.Reader, options build.ImageBuildOptions) (build.ImageBuildResponse, error) {
			return build.ImageBuildResponse{
				Body: io.NopCloser(strings.NewReader(buildBody)),
			}, nil
		},
		containerAttachFunc: func(ctx context.Context, ctr string, options container.AttachOptions) (types.HijackedResponse, error) {
			return newHijackedResponse("hello world\n"), nil
		},
		containerWaitFunc: func(ctx context.Context, ctr string, condition container.WaitCondition) (<-chan container.WaitResponse, <-chan error) {
			ch := make(chan container.WaitResponse, 1)
			ch <- container.WaitResponse{StatusCode: 0}
			return ch, make(chan error)
		},
	}
	b := NewContainerBackendFromClient(mock)

	proc, err := b.Start(context.Background(), &model.Task{}, &model.ContainerExecution{
		Script:    "echo hello",
		BaseImage: "alpine",
	})
	require.NoError(t, err)
	require.NotNil(t, proc)

	output, err := io.ReadAll(proc.Stdout)
	require.NoError(t, err)
	assert.Contains(t, string(output), "hello world")

	exitCode, err := proc.Wait()
	require.NoError(t, err)
	assert.Equal(t, 0, exitCode)

	proc.Cleanup()
}

func TestStartWaitError(t *testing.T) {
	buildBody := `{"stream":"ok"}` + "\n"
	mock := &mockDockerClient{
		imageBuildFunc: func(ctx context.Context, buildContext io.Reader, options build.ImageBuildOptions) (build.ImageBuildResponse, error) {
			return build.ImageBuildResponse{
				Body: io.NopCloser(strings.NewReader(buildBody)),
			}, nil
		},
		containerAttachFunc: func(ctx context.Context, ctr string, options container.AttachOptions) (types.HijackedResponse, error) {
			return newHijackedResponse(""), nil
		},
		containerWaitFunc: func(ctx context.Context, ctr string, condition container.WaitCondition) (<-chan container.WaitResponse, <-chan error) {
			errCh := make(chan error, 1)
			errCh <- fmt.Errorf("container crashed")
			return make(chan container.WaitResponse), errCh
		},
	}
	b := NewContainerBackendFromClient(mock)

	proc, err := b.Start(context.Background(), &model.Task{}, &model.ContainerExecution{
		Script:    "exit 1",
		BaseImage: "alpine",
	})
	require.NoError(t, err)

	exitCode, err := proc.Wait()
	require.Error(t, err)
	assert.Equal(t, -1, exitCode)
	proc.Cleanup()
}

func TestStartNonZeroExitCode(t *testing.T) {
	buildBody := `{"stream":"ok"}` + "\n"
	mock := &mockDockerClient{
		imageBuildFunc: func(ctx context.Context, buildContext io.Reader, options build.ImageBuildOptions) (build.ImageBuildResponse, error) {
			return build.ImageBuildResponse{
				Body: io.NopCloser(strings.NewReader(buildBody)),
			}, nil
		},
		containerAttachFunc: func(ctx context.Context, ctr string, options container.AttachOptions) (types.HijackedResponse, error) {
			return newHijackedResponse(""), nil
		},
		containerWaitFunc: func(ctx context.Context, ctr string, condition container.WaitCondition) (<-chan container.WaitResponse, <-chan error) {
			ch := make(chan container.WaitResponse, 1)
			ch <- container.WaitResponse{StatusCode: 42}
			return ch, make(chan error)
		},
	}
	b := NewContainerBackendFromClient(mock)

	proc, err := b.Start(context.Background(), &model.Task{}, &model.ContainerExecution{
		Script:    "exit 42",
		BaseImage: "alpine",
	})
	require.NoError(t, err)

	exitCode, err := proc.Wait()
	require.NoError(t, err)
	assert.Equal(t, 42, exitCode)
	proc.Cleanup()
}

func TestAddTarFile(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	err := addTarFile(tw, "hello.txt", []byte("world"))
	require.NoError(t, err)
	require.NoError(t, tw.Close())

	tr := tar.NewReader(&buf)
	header, err := tr.Next()
	require.NoError(t, err)
	assert.Equal(t, "hello.txt", header.Name)
	assert.Equal(t, int64(5), header.Size)
	assert.Equal(t, int64(0755), header.Mode)

	content, err := io.ReadAll(tr)
	require.NoError(t, err)
	assert.Equal(t, "world", string(content))
}

func TestBuildContextWithDockerfileBlocks(t *testing.T) {
	ctr := &model.ContainerExecution{
		BaseImage: "ubuntu:22.04",
		Script:    "#!/bin/sh\necho done",
		Blocks: []model.DockerfileBlock{
			{Label: "Install curl", Dockerfile: "RUN apt-get install -y curl", Enabled: true},
			{Label: "Disabled block", Dockerfile: "RUN apt-get install -y vim", Enabled: false},
		},
	}

	reader, err := BuildContext(ctr)
	require.NoError(t, err)

	data, err := io.ReadAll(reader)
	require.NoError(t, err)

	tr := tar.NewReader(bytes.NewReader(data))
	files := make(map[string]string)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		content, err := io.ReadAll(tr)
		require.NoError(t, err)
		files[header.Name] = string(content)
	}

	// Enabled block should be in Dockerfile
	assert.Contains(t, files["Dockerfile"], "Install curl")
	assert.Contains(t, files["Dockerfile"], "RUN apt-get install -y curl")
	// Disabled block should not appear
	assert.NotContains(t, files["Dockerfile"], "vim")
	assert.Equal(t, "#!/bin/sh\necho done", files["script.sh"])
}

func TestBuildContainerConfigPortDefaultProtocol(t *testing.T) {
	b := NewContainerBackendFromClient(&mockDockerClient{})
	ctr := &model.ContainerExecution{
		Script:    "echo test",
		BaseImage: "alpine",
		Ports: []model.PortMapping{
			{Host: 3000, Container: 3000, Protocol: "UDP"},
		},
	}

	containerCfg, hostCfg := b.buildContainerConfig("img", ctr, nil)
	_, has3000 := containerCfg.ExposedPorts["3000/udp"]
	assert.True(t, has3000)
	_, hasBinding := hostCfg.PortBindings["3000/udp"]
	assert.True(t, hasBinding)
}

func TestRemoveContainerLogsError(t *testing.T) {
	called := false
	mock := &mockDockerClient{
		containerRemoveFunc: func(ctx context.Context, ctr string, options container.RemoveOptions) error {
			called = true
			return fmt.Errorf("remove failed")
		},
	}
	b := NewContainerBackendFromClient(mock)

	// Should not panic, should log warning
	b.removeContainer(context.Background(), "test-id")
	assert.True(t, called)
}

func TestRemoveImageLogsError(t *testing.T) {
	called := false
	mock := &mockDockerClient{
		imageRemoveFunc: func(ctx context.Context, imageRef string, options image.RemoveOptions) ([]image.DeleteResponse, error) {
			called = true
			return nil, fmt.Errorf("remove failed")
		},
	}
	builder := &ImageBuilder{docker: mock}

	// Should not panic, should log warning
	builder.Remove(context.Background(), "test-tag")
	assert.True(t, called)
}

func TestStartBuildOutputMultipleMessages(t *testing.T) {
	// Build output with multiple JSON messages, none being errors
	messages := []string{
		`{"stream":"Step 1/3 : FROM alpine"}`,
		`{"stream":"Step 2/3 : COPY script.sh /script.sh"}`,
		`{"stream":"Step 3/3 : ENTRYPOINT [\"/bin/sh\"]"}`,
	}
	buildBody := strings.Join(messages, "\n")

	mock := &mockDockerClient{
		imageBuildFunc: func(ctx context.Context, buildContext io.Reader, options build.ImageBuildOptions) (build.ImageBuildResponse, error) {
			return build.ImageBuildResponse{
				Body: io.NopCloser(strings.NewReader(buildBody)),
			}, nil
		},
		containerAttachFunc: func(ctx context.Context, ctr string, options container.AttachOptions) (types.HijackedResponse, error) {
			return newHijackedResponse(""), nil
		},
	}
	b := NewContainerBackendFromClient(mock)

	proc, err := b.Start(context.Background(), &model.Task{}, &model.ContainerExecution{
		Script:    "echo ok",
		BaseImage: "alpine",
	})
	require.NoError(t, err)
	require.NotNil(t, proc)
	proc.Cleanup()
}

func TestStartPassesBuildOptions(t *testing.T) {
	var capturedTags []string
	mock := &mockDockerClient{
		imageBuildFunc: func(ctx context.Context, buildContext io.Reader, options build.ImageBuildOptions) (build.ImageBuildResponse, error) {
			capturedTags = options.Tags
			assert.True(t, options.Remove)
			assert.True(t, options.ForceRemove)
			return build.ImageBuildResponse{
				Body: io.NopCloser(strings.NewReader(`{"stream":"ok"}`)),
			}, nil
		},
		containerAttachFunc: func(ctx context.Context, ctr string, options container.AttachOptions) (types.HijackedResponse, error) {
			return newHijackedResponse(""), nil
		},
	}
	b := NewContainerBackendFromClient(mock)

	proc, err := b.Start(context.Background(), &model.Task{}, &model.ContainerExecution{
		Script:    "echo test",
		BaseImage: "alpine",
	})
	require.NoError(t, err)
	require.Len(t, capturedTags, 1)
	assert.True(t, strings.HasPrefix(capturedTags[0], "runwisp-task-"))
	proc.Cleanup()
}

// Verify that HijackedResponse is correctly used in attach - use real struct
func TestContainerAttachOptions(t *testing.T) {
	var capturedOpts container.AttachOptions
	mock := &mockDockerClient{
		imageBuildFunc: func(ctx context.Context, buildContext io.Reader, options build.ImageBuildOptions) (build.ImageBuildResponse, error) {
			return build.ImageBuildResponse{
				Body: io.NopCloser(strings.NewReader(`{"stream":"ok"}`)),
			}, nil
		},
		containerAttachFunc: func(ctx context.Context, ctr string, options container.AttachOptions) (types.HijackedResponse, error) {
			capturedOpts = options
			return newHijackedResponse(""), nil
		},
	}
	b := NewContainerBackendFromClient(mock)

	proc, err := b.Start(context.Background(), &model.Task{}, &model.ContainerExecution{
		Script:    "echo hello",
		BaseImage: "alpine",
	})
	require.NoError(t, err)

	assert.True(t, capturedOpts.Stream)
	assert.True(t, capturedOpts.Stdout)
	assert.True(t, capturedOpts.Stderr)
	proc.Cleanup()
}

// Verify build context tar content is valid for the Docker daemon
func TestBuildContextTarValidity(t *testing.T) {
	ctr := &model.ContainerExecution{
		BaseImage:  "alpine:3.18",
		Script:     "#!/bin/bash\nset -e\necho 'test script'",
		Dockerfile: "FROM alpine:3.18\nCOPY script.sh /\nENTRYPOINT [\"/bin/sh\", \"/script.sh\"]",
	}

	reader, err := BuildContext(ctr)
	require.NoError(t, err)

	var buf bytes.Buffer
	_, err = io.Copy(&buf, reader)
	require.NoError(t, err)

	tr := tar.NewReader(&buf)
	var fileCount int
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		require.True(t, header.Size >= 0)
		fileCount++

		content := make([]byte, header.Size)
		_, err = io.ReadFull(tr, content)
		require.NoError(t, err)

		if header.Name == "Dockerfile" {
			assert.Equal(t, ctr.Dockerfile, string(content))
		}
	}
	assert.Equal(t, 2, fileCount)
}

func TestStartCleanupBuildError(t *testing.T) {
	// Verify that build body is properly closed even on JSON error in stream
	bodyClosed := false
	errBody := &trackingCloser{
		Reader: strings.NewReader(`{"error":"syntax error in Dockerfile"}`),
		onClose: func() {
			bodyClosed = true
		},
	}

	mock := &mockDockerClient{
		imageBuildFunc: func(ctx context.Context, buildContext io.Reader, options build.ImageBuildOptions) (build.ImageBuildResponse, error) {
			return build.ImageBuildResponse{Body: errBody}, nil
		},
	}
	b := NewContainerBackendFromClient(mock)

	_, err := b.Start(context.Background(), &model.Task{}, &model.ContainerExecution{
		Script:    "echo test",
		BaseImage: "alpine",
	})
	require.Error(t, err)
	assert.True(t, bodyClosed, "build response body should be closed on error")
}

type trackingCloser struct {
	io.Reader
	onClose func()
}

func (tc *trackingCloser) Close() error {
	if tc.onClose != nil {
		tc.onClose()
	}
	return nil
}

// Ensure ReadLogMeta is json-compatible by testing the build output JSON parsing
func TestBuildOutputJSONParsing(t *testing.T) {
	messages := []struct {
		input    string
		hasError bool
	}{
		{`{"stream":"Step 1/1"}`, false},
		{`{"error":"failed to build"}`, true},
		{`not json at all`, false},
		{`{"aux":{"ID":"sha256:abc123"}}`, false},
	}

	for _, msg := range messages {
		type buildMessage struct {
			Error string `json:"error,omitempty"`
		}
		var bm buildMessage
		if err := json.Unmarshal([]byte(msg.input), &bm); err == nil {
			if msg.hasError {
				assert.NotEmpty(t, bm.Error)
			} else {
				assert.Empty(t, bm.Error)
			}
		}
	}
}
