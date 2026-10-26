// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
)

// upContainer is a running container that `docker compose up` started.
type upContainer struct {
	id, name string
}

// composeUpContainer returns the container `docker compose up` runs for ce's
// service in the instanceIndex-th slot (by container name), if any. Only a
// run-mode service attaches: a one-off task next to an `up` stack (a
// migration) is a normal pattern, and stack and exec modes never start a
// second container of the service. Compose
// labels its own containers with project and service, and marks `compose run`
// ones, RunWisp's included, as oneoff, so those never match. A unit without a
// project name has nothing to match on. A Docker error fails open: the caller
// starts its own container, as it would without this check.
func (b *ComposeBackend) composeUpContainer(ctx context.Context, ce *model.ComposeExecution, task *model.Task, instanceIndex int) (upContainer, bool) {
	if ce.Mode != model.ComposeModeRun || !task.Kind.IsService() || ce.ProjectName == "" || ce.Service == "" {
		return upContainer{}, false
	}
	ctx, cancel := context.WithTimeout(ctx, composeHousekeepingTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, b.dockerCmd, "ps",
		"--filter", "label=com.docker.compose.project="+ce.ProjectName,
		"--filter", "label=com.docker.compose.service="+ce.Service,
		"--filter", "label=com.docker.compose.oneoff=False",
		"--format", "{{.ID}} {{.Names}}").Output()
	if err != nil {
		slog.Warn("compose: could not check for a docker compose up container, starting a managed one",
			"project", ce.ProjectName, "service", ce.Service, "err", err)
		return upContainer{}, false
	}
	var found []upContainer
	for _, line := range strings.Split(string(out), "\n") {
		if id, name, ok := strings.Cut(strings.TrimSpace(line), " "); ok {
			found = append(found, upContainer{id: id, name: name})
		}
	}
	slices.SortFunc(found, func(a, b upContainer) int { return strings.Compare(a.name, b.name) })
	if instanceIndex >= len(found) {
		return upContainer{}, false
	}
	return found[instanceIndex], true
}

// attach follows c's output as the run instead of starting a second container
// next to it, which for a database means two containers on one volume. The
// run ends when c does, with c's own exit code. A stop or restart ends the
// log follow and then stops c, so the next start finds no `up` container and
// launches a RunWisp-managed one: ownership moves over without a duplicate.
func (b *ComposeBackend) attach(ctx context.Context, task *model.Task, c upContainer) (*Process, error) {
	// --since now: the run shows what the container prints from here on, not
	// the history it logged before RunWisp got to it.
	since := time.Now().UTC().Format(time.RFC3339)
	cmd := exec.CommandContext(ctx, b.dockerCmd, "logs", "-f", "--since", since, c.id)
	proc, err := startCmd(cmd, task.GracefulStopValue(), syscall.SIGTERM, nil, "attach to docker compose container")
	if err != nil {
		return nil, err
	}
	// The process tree is only the docker CLI; the workload is the container.
	proc.Pid, proc.Rusage = 0, nil
	followWait := proc.Wait
	proc.Wait = func() (int, error) {
		code, err := followWait()
		return b.endAttached(c.id, task.GracefulStopValue(), code, err)
	}
	proc.Notice = fmt.Sprintf("Attached to %s, started outside RunWisp by docker compose up. "+
		"A stop or restart replaces it with a RunWisp-managed container.", c.name)
	return proc, nil
}

// endAttached runs once the log follow has ended. If the container is still
// running, the follow was stopped (a stop or restart), so the container is
// stopped too, given grace to exit. The run then reports the container's exit
// code. When Docker can't say, the follow's own result stands.
func (b *ComposeBackend) endAttached(id string, grace time.Duration, followCode int, followErr error) (int, error) {
	running, code, err := b.containerState(id)
	if err != nil {
		return followCode, followErr
	}
	if running {
		ctx, cancel := context.WithTimeout(context.Background(), grace+composeHousekeepingTimeout)
		secs := strconv.Itoa(int(math.Ceil(grace.Seconds())))
		if err := exec.CommandContext(ctx, b.dockerCmd, "stop", "-t", secs, id).Run(); err != nil {
			slog.Warn("compose: could not stop the attached container", "id", id, "err", err)
		}
		cancel()
		if _, code, err = b.containerState(id); err != nil {
			return followCode, followErr
		}
	}
	return code, nil
}

// containerState reports whether container id is running and its last exit
// code.
func (b *ComposeBackend) containerState(id string) (running bool, exitCode int, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), composeHousekeepingTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, b.dockerCmd, "inspect", "-f", "{{.State.Running}} {{.State.ExitCode}}", id).Output()
	if err != nil {
		slog.Warn("compose: could not inspect the attached container", "id", id, "err", err)
		return false, 0, err
	}
	state, codeStr, _ := strings.Cut(strings.TrimSpace(string(out)), " ")
	exitCode, err = strconv.Atoi(codeStr)
	return state == "true", exitCode, err
}
