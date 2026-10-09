// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"context"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"log/slog"

	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
)

// composeAvailableTimeout caps the `docker compose version` probe we use to
// decide whether the backend is wired up at all.
const composeAvailableTimeout = 2 * time.Second

// composeHousekeepingTimeout bounds the `docker ps`/`docker rm -f` calls made by
// reclaim-on-start and Process.Cleanup (both via removeManagedRun). Without
// a deadline a hung (not merely unreachable, that fails fast) Docker/Podman
// engine would stall a run before it launches, or stall shutdown: Cleanup runs
// in the goroutine the shutdown coordinator waits on after ForceKill. A var (not
// const) so tests can shrink it instead of waiting out the real 10s.
var composeHousekeepingTimeout = 10 * time.Second

// composeExecShell is the interpreter exec-mode hands the script to *inside the
// target container*. It is not the daemon's `shell` setting: that key is
// rejected on compose-backed units because it configures a host process, and
// the host's shell path says nothing about what exists in someone else's image.
// /bin/sh is the one path a POSIX image is required to provide.
const composeExecShell = "/bin/sh"

// Ownership labels stamped on every services-mode container so the daemon can
// recognise and reclaim containers it launched in a previous lifetime. The
// instance-fp label scopes reclaim/cleanup to *this* daemon, so two daemons
// that share a compose project name never delete each other's containers.
const (
	labelManaged    = "com.runwisp.managed"
	labelTask       = "com.runwisp.task"
	labelInstance   = "com.runwisp.instance"
	labelInstanceFP = "com.runwisp.instance-fp"
	labelRun        = "com.runwisp.run"
)

// ComposeBackend executes docker-compose-declared services by shelling out to
// the `docker compose` CLI. The CLI gates this entirely: composespec is used
// at config-load to enumerate services (offline), but every actual container
// spawn goes through `docker compose run --rm` (or `up` in stack mode).
type ComposeBackend struct {
	// dockerCmd is the binary name; "docker" by default. Tests inject a shim.
	dockerCmd string
	// fingerprint identifies this daemon instance; stamped on managed
	// containers and used to scope reclaim/cleanup to our own containers.
	fingerprint string

	// probeMu guards avail. The probe runs under it so concurrent first starts
	// share one `docker compose version` call.
	probeMu sync.Mutex
	avail   bool

	// mu guards the maps below, which are created lazily so a bare struct
	// literal is usable.
	mu sync.Mutex
	// namesInUse holds the container names of runs this backend has started and
	// not yet cleaned up, so an overlapping run of the same slot picks another.
	namesInUse map[string]bool
	// slots serialises each (task, instance) slot's reclaim of stale
	// containers and tracks what is live and what is left to reclaim.
	slots map[string]*composeSlot
}

// composeSlot counts a slot's runs between reclaim and cleanup, and holds the
// runs whose cleanup failed to remove their container (run ID to the name the
// container may still hold).
type composeSlot struct {
	mu      sync.Mutex
	live    int
	orphans map[string]string
}

// NewComposeBackend returns a ComposeBackend ready for use. Availability is
// probed on the first Start, not here, so the daemon boots even when the docker
// CLI is missing or slow. fingerprint scopes managed-container reclaim to this
// daemon instance.
func NewComposeBackend(fingerprint string) *ComposeBackend {
	return &ComposeBackend{dockerCmd: "docker", fingerprint: fingerprint}
}

// available probes `docker compose version` with a short timeout. Returns
// false when the binary is missing, the daemon is unreachable, or the call
// exceeds composeAvailableTimeout. Only success is cached: a transient failure
// (docker still coming up) must not disable compose for the daemon's lifetime,
// so it re-probes until the probe passes.
func (b *ComposeBackend) available(ctx context.Context) bool {
	b.probeMu.Lock()
	defer b.probeMu.Unlock()
	if !b.avail {
		probeCtx, cancel := context.WithTimeout(ctx, composeAvailableTimeout)
		defer cancel()
		b.avail = exec.CommandContext(probeCtx, b.dockerCmd, "compose", "version").Run() == nil
	}
	return b.avail
}

func (b *ComposeBackend) Start(ctx context.Context, task *model.Task, run *model.Run, def model.ExecutionDef) (*Process, error) {
	ce, ok := def.(*model.ComposeExecution)
	if !ok {
		return nil, fmt.Errorf("ComposeBackend received non-compose execution: %s", def.ExecType())
	}
	if ce.File == "" {
		return nil, fmt.Errorf("compose execution missing file path")
	}
	if !b.available(ctx) {
		return nil, fmt.Errorf("docker compose unavailable: install Docker (with the compose plugin) or check that `docker compose version` succeeds")
	}

	instanceIndex := 0
	if run != nil {
		instanceIndex = run.InstanceIndex
	}

	// Services mode uses a deterministic --name; reclaim any container our
	// previous daemon life left behind for this slot before launching, so a
	// kill -9 / restart can't collide with our own orphan. Label-scoped to this
	// daemon's fingerprint, so a user's unrelated same-named container is never
	// touched. Stack mode lets compose own container lifecycle, and exec mode
	// targets a container we never created, so both are exempt.
	runID := ""
	if run != nil {
		runID = run.ID
	}
	containerName := ""
	var slot *composeSlot
	if ce.Mode == model.ComposeModeRun {
		reclaimCtx, cancel := context.WithTimeout(ctx, composeHousekeepingTimeout)
		slot = b.reclaim(reclaimCtx, task.Name, instanceIndex)
		cancel()
		containerName = b.claimName(composeContainerName(ce.ProjectName, ce.Service, instanceIndex), runID)
	}

	args := buildComposeArgs(ce, task, run, b.fingerprint, containerName)
	cmd := exec.CommandContext(ctx, b.dockerCmd, args...)
	if ce.WorkingDir != "" {
		cmd.Dir = ce.WorkingDir
	}

	// Compose CLI inherits the daemon's env (minus RUNWISP_* daemon secrets,
	// which buildProcessEnv strips) so users' DOCKER_HOST etc. work without
	// leaking the admin password / station token into every container's build
	// environment. Task env/secrets/params are then appended so the value-less
	// `-e KEY` flags (see buildComposeArgs) resolve their values from the CLI's
	// own environment — keeping secret values off argv (and out of `ps` output).
	// Stack mode forwards env via compose itself, not `-e`, so it's exempt.
	cmd.Env = buildProcessEnv(os.Environ())
	if ce.Mode != model.ComposeModeStack {
		cmd.Env = append(cmd.Env, composeEnv(task, run, instanceIndex)...)
	}

	proc, err := startCmd(cmd, task.GracefulStopValue(), signalFromName(task.StopSignal), nil, "start docker compose")
	if err != nil {
		if slot != nil {
			b.finishRun(slot, runID, containerName, nil)
		}
		return nil, err
	}
	// The process tree here is only the compose CLI; the workload lives in the
	// container, so there is nothing meaningful to measure.
	proc.Pid, proc.Rusage = 0, nil

	// Force-remove the container on exit, mirroring ContainerBackend's cleanup.
	// `--rm` already covers the clean-exit case; this catches the SIGKILL /
	// graceful-stop overrun where `docker compose run` is killed and `--rm`
	// never fires. Background context: the run's ctx is already cancelled by the
	// time cleanup runs. Reclaim-on-start (above) is the backstop for kill -9 of
	// the daemon itself, where cleanup never gets to run.
	//
	// Exec mode is exempt for a much sharper reason than stack mode: the target
	// container belongs to the user, so tearing it down because a cron task
	// inside it overran would take their application with it.
	if ce.Mode == model.ComposeModeRun {
		taskName := task.Name
		proc.Cleanup = func() {
			var err error
			// Without a run ID the label filter would match the whole slot,
			// overlapping runs included; production runs always carry one.
			if runID != "" {
				cleanupCtx, cancel := context.WithTimeout(context.Background(), composeHousekeepingTimeout)
				defer cancel()
				err = b.removeManagedRun(cleanupCtx, taskName, instanceIndex, runID)
			}
			b.finishRun(slot, runID, containerName, err)
		}
	}

	return proc, nil
}

// claimName returns the --name for a run of one slot: the plain name unless an
// earlier run of the slot still holds it (an overlapping run, or a retry that
// starts before the previous attempt is reaped), in which case the run ID is
// appended so the two never share, and never remove, one container.
func (b *ComposeBackend) claimName(base, runID string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.namesInUse == nil {
		b.namesInUse = make(map[string]bool)
	}
	name := base
	if b.namesInUse[base] && runID != "" {
		name = base + "_" + runID
	}
	b.namesInUse[name] = true
	return name
}

func (b *ComposeBackend) releaseName(name string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.namesInUse, name)
}

func (b *ComposeBackend) slot(taskName string, instanceIndex int) *composeSlot {
	key := taskName + "\x00" + strconv.Itoa(instanceIndex)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.slots == nil {
		b.slots = make(map[string]*composeSlot)
	}
	slot := b.slots[key]
	if slot == nil {
		slot = &composeSlot{}
		b.slots[key] = slot
	}
	return slot
}

// reclaim removes the slot's stale containers before a start and counts the
// start as live. While no run of this process is live in the slot, every
// container carrying its labels is stale (a prior daemon life's, or one whose
// cleanup failed), so all go. Otherwise those may be live overlapping runs,
// whose own Cleanup handles them, and only failed cleanups are retried. Docker
// failures leave the work for the next start.
func (b *ComposeBackend) reclaim(ctx context.Context, taskName string, instanceIndex int) *composeSlot {
	slot := b.slot(taskName, instanceIndex)
	// Held across the reclaim so a concurrent start of the same slot cannot
	// launch its container while this one is still listing.
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if slot.live == 0 && b.removeManagedRun(ctx, taskName, instanceIndex, "") == nil {
		for runID, name := range slot.orphans {
			delete(slot.orphans, runID)
			b.releaseName(name)
		}
	}
	for runID, name := range slot.orphans {
		if b.removeManagedRun(ctx, taskName, instanceIndex, runID) == nil {
			delete(slot.orphans, runID)
			b.releaseName(name)
		}
	}
	slot.live++
	return slot
}

// finishRun ends a run's hold on its slot. When removing its container failed,
// the container may still hold the name, so the name stays claimed (the next
// run picks another) until a later reclaim removes it.
func (b *ComposeBackend) finishRun(slot *composeSlot, runID, containerName string, removeErr error) {
	slot.mu.Lock()
	defer slot.mu.Unlock()
	slot.live--
	if removeErr == nil {
		b.releaseName(containerName)
		return
	}
	if slot.orphans == nil {
		slot.orphans = make(map[string]string)
	}
	slot.orphans[runID] = containerName
}

// removeManagedRun force-removes the containers this daemon launched for
// (task, instanceIndex). With a runID it removes only that run's container
// (cleanup-on-exit, when `--rm` never fired); with "" it removes every one of
// the slot's containers (reclaim-on-start of a prior life's orphans). The label
// filter, including this daemon's fingerprint, guarantees it only ever removes
// RunWisp's own containers; a genuine name clash with a non-managed container
// still fails loudly at create, which is correct. A docker failure is logged
// and returned; reclaim is best-effort and must never block a run.
func (b *ComposeBackend) removeManagedRun(ctx context.Context, taskName string, instanceIndex int, runID string) error {
	ids, err := b.listManagedContainers(ctx, taskName, instanceIndex, runID)
	if err != nil || len(ids) == 0 {
		return err
	}
	return b.removeContainers(ctx, ids)
}

// listManagedContainers returns the IDs of containers carrying this daemon's
// ownership labels for (task, instanceIndex), narrowed to one run when runID is
// set.
func (b *ComposeBackend) listManagedContainers(ctx context.Context, taskName string, instanceIndex int, runID string) ([]string, error) {
	args := []string{"ps", "-aq",
		"--filter", "label=" + labelTask + "=" + taskName,
		"--filter", "label=" + labelInstance + "=" + strconv.Itoa(instanceIndex),
		"--filter", "label=" + labelInstanceFP + "=" + b.fingerprint,
	}
	if runID != "" {
		args = append(args, "--filter", "label="+labelRun+"="+runID)
	}
	out, err := exec.CommandContext(ctx, b.dockerCmd, args...).Output()
	if err != nil {
		slog.Warn("compose: could not list managed containers for reclaim",
			"task", taskName, "instance", instanceIndex, "err", err)
		return nil, err
	}
	return parseContainerIDs(out), nil
}

// removeContainers force-removes the given container IDs in one `docker rm -f`.
func (b *ComposeBackend) removeContainers(ctx context.Context, ids []string) error {
	args := append([]string{"rm", "-f"}, ids...)
	err := exec.CommandContext(ctx, b.dockerCmd, args...).Run()
	if err != nil {
		slog.Warn("compose: could not remove managed container(s)",
			"ids", strings.Join(ids, ","), "err", err)
	}
	return err
}

// parseContainerIDs splits `docker ps -aq` output (one ID per line) into a
// trimmed, empty-free slice.
func parseContainerIDs(out []byte) []string {
	var ids []string
	for _, line := range strings.Split(string(out), "\n") {
		if id := strings.TrimSpace(line); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// buildComposeArgs assembles the argv tail (after the docker binary) for
// either per-service (`run --rm`) or stack-mode (`up --abort-on-container-exit`)
// invocations. RUNWISP_INSTANCE_INDEX + task.Env + task.Secrets flow into
// the target container via repeated value-less `-e KEY` flags, deterministically
// ordered; docker resolves each value from the CLI's environment (injected in
// Start), so no value ever appears on argv.
// fingerprint scopes the ownership labels stamped on the container, and
// containerName is the --name used in run mode.
func buildComposeArgs(ce *model.ComposeExecution, task *model.Task, run *model.Run, fingerprint, containerName string) []string {
	args := []string{"compose", "-f", ce.File}
	if ce.ProjectName != "" {
		args = append(args, "-p", ce.ProjectName)
	}
	for _, p := range ce.Profiles {
		args = append(args, "--profile", p)
	}
	for _, ef := range ce.EnvFile {
		args = append(args, "--env-file", ef)
	}

	switch ce.Mode {
	case model.ComposeModeStack:
		args = append(args, "up", "--abort-on-container-exit", "--no-log-prefix")
	case model.ComposeModeExec:
		args = appendComposeExecArgs(args, ce, task, run)
	default:
		args = appendComposeRunArgs(args, ce, task, run, fingerprint, containerName)
	}
	return args
}

// appendComposeExecArgs appends the `compose exec`-specific flags: the command
// runs inside the service's already-running container, so none of the
// create-time flags (--rm, --name, --label, --service-ports, --pull, --no-deps)
// apply and none are passed.
//
// -T is explicit rather than implied. Compose does detect the absent TTY on its
// own, but a RunWisp run has no terminal and we never want to depend on that
// detection: with a TTY allocated, stderr folds into stdout and every captured
// line gains a trailing \r, which would corrupt the run log.
//
// The command goes through `sh -e -c` for the same reason the host shell backend
// does it (see executor.shellArgs): a multi-line script whose middle line fails
// must fail the run rather than inherit the last command's exit code. That makes
// fail-fast uniform across backends — the target container needs a POSIX `sh`,
// which every image carrying a shell has.
func appendComposeExecArgs(args []string, ce *model.ComposeExecution, task *model.Task, run *model.Run) []string {
	args = append(args, "exec", "-T")

	instanceIndex := 0
	if run != nil {
		instanceIndex = run.InstanceIndex
	}
	for _, kv := range composeEnvFlags(task, run, instanceIndex) {
		args = append(args, "-e", kv)
	}

	// Arg/option/flag parameters are appended to the script text shell-quoted,
	// exactly as the host shell backend does it (executor.appendArgTokens), so a
	// task reads the same whether it runs on the host or inside a container.
	var runParams map[string]string
	if run != nil {
		runParams = run.Params
	}
	script := appendArgTokens(ce.Command, model.ParamArgTokens(task.Parameters, runParams))

	return append(args, ce.Service, composeExecShell, "-e", "-c", script)
}

// appendComposeRunArgs appends the `compose run`-specific flags (one-off
// container): teardown, deps/pull policy, the daemon-owned container name and
// ownership labels, per-execution env, the service, and the per-execution
// arg/option/flag tokens.
func appendComposeRunArgs(args []string, ce *model.ComposeExecution, task *model.Task, run *model.Run, fingerprint, containerName string) []string {
	args = append(args, "run", "--rm", "--service-ports", "--use-aliases")
	if !ce.WithDeps {
		args = append(args, "--no-deps")
	}
	if ce.Pull != "" && ce.Pull != model.ComposePullMissing {
		args = append(args, "--pull", ce.Pull)
	}
	instanceIndex := 0
	if run != nil {
		instanceIndex = run.InstanceIndex
	}
	args = append(args, "--name", containerName)
	runID := ""
	if run != nil {
		runID = run.ID
	}
	for _, l := range composeManagedLabels(task.Name, instanceIndex, fingerprint, runID) {
		args = append(args, "--label", l)
	}
	for _, k := range composeEnvFlags(task, run, instanceIndex) {
		args = append(args, "-e", k)
	}
	args = append(args, ce.Service)
	// NOTE: these are not the append-to-the-command semantics the shell and
	// exec-mode backends have. `compose run SERVICE [COMMAND] [ARGS…]` treats the
	// first positional after the service as COMMAND, so these tokens replace the
	// service's compose-declared command (and land as arguments to the image's
	// ENTRYPOINT when it has one). Documented on the tasks config page.
	var runParams map[string]string
	if run != nil {
		runParams = run.Params
	}
	return append(args, model.ParamArgTokens(task.Parameters, runParams)...)
}

// composeManagedLabels returns the ordered ownership labels stamped on every
// services-mode container. instanceFP scopes reclaim/cleanup to this daemon so
// two daemons sharing a compose project never delete each other's containers.
// Ordering is fixed so argv stays stable for tests.
func composeManagedLabels(taskName string, instanceIndex int, instanceFP, runID string) []string {
	labels := []string{
		labelManaged + "=true",
		labelTask + "=" + taskName,
		labelInstance + "=" + strconv.Itoa(instanceIndex),
		labelInstanceFP + "=" + instanceFP,
	}
	if runID != "" {
		labels = append(labels, labelRun+"="+runID)
	}
	return labels
}

// composeContainerName mirrors docker compose's own naming (`<project>_<svc>_<index>`)
// so `docker compose ps` shows each RunWisp instance as a separately named
// container. Falls back to shorter names if the project or service is empty.
func composeContainerName(project, service string, idx int) string {
	switch {
	case project == "" && service == "":
		return ""
	case project == "":
		return fmt.Sprintf("%s_%d", service, idx)
	case service == "":
		return fmt.Sprintf("%s_%d", project, idx)
	default:
		return fmt.Sprintf("%s_%s_%d", project, service, idx)
	}
}

// composeMergedEnv builds the deterministic variable set forwarded into the
// target container. RUNWISP_INSTANCE_INDEX is always injected; task.Env wins
// over the daemon's environment because we only forward the user's declared
// variables, not os.Environ(). Secrets override plain env, and the per-run
// param env layer is applied last so manual intent wins (collisions are
// rejected at config load, so order is immaterial in valid configs).
func composeMergedEnv(task *model.Task, run *model.Run, instanceIndex int) map[string]string {
	merged := map[string]string{
		instanceIndexEnvKey: strconv.Itoa(instanceIndex),
	}
	maps.Copy(merged, task.Env)
	maps.Copy(merged, task.Secrets)
	var runParams map[string]string
	if run != nil {
		runParams = run.Params
	}
	maps.Copy(merged, model.ParamEnvLayer(task.Parameters, runParams))
	return merged
}

// composeEnvFlags returns the deterministically ordered variable NAMES to
// forward into the container as value-less `-e KEY` flags. Docker's `-e KEY`
// form reads each value from the calling process's environment — which Start
// populates via composeEnv — so secret values never appear on argv (and thus
// never in `ps` output).
func composeEnvFlags(task *model.Task, run *model.Run, instanceIndex int) []string {
	return slices.Sorted(maps.Keys(composeMergedEnv(task, run, instanceIndex)))
}

// composeEnv returns the forwarded variables as deterministically ordered
// KEY=VALUE pairs, for appending to the docker CLI child process's environment
// in Start. Pairing with composeEnvFlags' value-less `-e KEY` flags, this is
// how the actual values reach the container without ever touching argv.
func composeEnv(task *model.Task, run *model.Run, instanceIndex int) []string {
	merged := composeMergedEnv(task, run, instanceIndex)
	keys := slices.Sorted(maps.Keys(merged))
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = k + "=" + merged[k]
	}
	return out
}
