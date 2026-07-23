// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package executor

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"log/slog"

	"github.com/moby/moby/client"
	"github.com/oklog/ulid/v2"
	"github.com/runwisp/runwisp/internal/model"
)

// ImageBuilder handles Docker image creation from execution definitions.
type ImageBuilder struct {
	docker dockerClient
}

func (b *ImageBuilder) Build(ctx context.Context, ctr *model.ContainerExecution) (string, error) {
	buildCtx, err := BuildContext(ctr)
	if err != nil {
		return "", fmt.Errorf("build context: %w", err)
	}

	imageTag := "runwisp-task-" + ulid.Make().String()

	buildResp, err := b.docker.ImageBuild(ctx, buildCtx, client.ImageBuildOptions{
		Tags:        []string{imageTag},
		Remove:      true,
		ForceRemove: true,
	})
	if err != nil {
		return "", fmt.Errorf("docker build: %w", err)
	}

	decoder := json.NewDecoder(buildResp.Body)
	type buildMessage struct {
		Error string `json:"error,omitempty"`
	}
	for {
		var msg buildMessage
		if err := decoder.Decode(&msg); err != nil {
			if err == io.EOF {
				break
			}
			// A non-EOF decode error means the stream is malformed; the decoder
			// does not advance past a syntax error, so continuing would busy-loop
			// forever on the same bytes. Bail out and surface the failure.
			buildResp.Body.Close()
			return "", fmt.Errorf("docker build: decode response stream: %w", err)
		}
		if msg.Error != "" {
			buildResp.Body.Close()
			return "", fmt.Errorf("docker build failed: %s", strings.TrimSpace(msg.Error))
		}
	}
	buildResp.Body.Close()

	return imageTag, nil
}

// Remove deletes a previously built image.
func (b *ImageBuilder) Remove(ctx context.Context, imageTag string) {
	if _, err := b.docker.ImageRemove(ctx, imageTag, client.ImageRemoveOptions{Force: true}); err != nil {
		slog.Warn("Failed to remove image", "tag", imageTag, "err", err)
	}
}

// BuildContext creates a tar archive containing the Dockerfile and script
// for the given container execution.
func BuildContext(ctr *model.ContainerExecution) (io.Reader, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	dockerfile := ctr.BuildDockerfile()
	if err := addTarFile(tw, "Dockerfile", []byte(dockerfile)); err != nil {
		return nil, err
	}

	if err := addTarFile(tw, "script.sh", []byte(ctr.Script)); err != nil {
		return nil, err
	}

	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("close tar: %w", err)
	}

	return &buf, nil
}

func addTarFile(tw *tar.Writer, name string, data []byte) error {
	header := &tar.Header{
		Name: name,
		Size: int64(len(data)),
		Mode: 0755,
	}
	if err := tw.WriteHeader(header); err != nil {
		return fmt.Errorf("write tar header %s: %w", name, err)
	}
	if _, err := tw.Write(data); err != nil {
		return fmt.Errorf("write tar data %s: %w", name, err)
	}
	return nil
}
