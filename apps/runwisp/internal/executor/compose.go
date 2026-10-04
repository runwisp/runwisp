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

	"github.com/runwisp/runwisp/internal/model"
)

// composeAvailableTimeout caps the `docker compose version` probe we use to
// decide whether the backend is wired up at all. Short on purpose — when the
// CLI is installed it answers in milliseconds, and when it isn't there's no
// payoff to a longer wait.
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

	// mu guards the maps below, which are created lazily so a bare struct
	// literal is usable.
	mu sync.Mutex
	// namesInUse holds the container names of runs this backend has started and
	// not yet cleaned up, so an overlapping run of the same slot picks another.
	namesInUse map[string]bool
	// slots serialises and remembers the one-time reclaim of each (task,
	// instance) slot's orphans from a prior daemon life.
	slots map[string]*composeSlot
}

// composeSlot records whether a slot's stale containers were already reclaimed.
type composeSlot struct {
	mu        sync.Mutex
	reclaimed bool
}

// NewComposeBackend returns a ComposeBackend ready for use. Availability is
// not probed eagerly — wrap in NewLazyComposeBackend when you want startup
// to survive a missing or slow docker CLI. fingerprint scopes managed-container
// reclaim to this daemon instance.
func NewComposeBackend(fingerprint string) *ComposeBackend {
	return &ComposeBackend{dockerCmd: "docker", fingerprint: fingerprint}
}

// Available probes `docker compose version` with a short timeout. Returns
// false when the binary is missing, the daemon is unreachable, or the call
// exceeds composeAvailableTimeout.
func (b *ComposeBackend) Available(ctx context.Context) bool {
	if b == nil {
		return false
	}
	probeCtx, cancel := context.WithTimeout(ctx, composeAvailableTimeout)
	defer cancel()
	return exec.CommandContext(probeCtx, b.dockerCmd, "compose", "version").Run() == nil
}

func (b *ComposeBackend) Start(ctx context.Context, task *model.Task, run *model.Run, def model.ExecutionDef) (*Process, error) {
	ce, ok := def.(*model.ComposeExecution)
	if !ok {
		return nil, fmt.Errorf("ComposeBackend received non-compose execution: %s", def.ExecType())
	}
	if ce.File == "" {
		return nil, fmt.Errorf("compose execution missing file path")
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
	if ce.Mode == model.ComposeModeRun {
		reclaimCtx, cancel := context.WithTimeout(ctx, composeHousekeepingTimeout)
		b.reclaimOnce(reclaimCtx, task.Name, instanceIndex)
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
		b.releaseName(containerName)
		return nil, err
	}

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
			cleanupCtx, cancel := context.WithTimeout(context.Background(), composeHousekeepingTimeout)
			defer cancel()
			b.removeManagedRun(cleanupCtx, taskName, instanceIndex, runID)
			b.releaseName(containerName)
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

// reclaimOnce removes the slot's containers left by a prior daemon life, the
// first time this process starts the slot. Later starts must not repeat it:
// by then any container carrying the slot's labels may be a live overlapping
// run of this process, whose own Cleanup handles its removal.
func (b *ComposeBackend) reclaimOnce(ctx context.Context, taskName string, instanceIndex int) {
	key := taskName + "\x00" + strconv.Itoa(instanceIndex)
	b.mu.Lock()
	if b.slots == nil {
		b.slots = make(map[string]*composeSlot)
	}
	slot := b.slots[key]
	if slot == nil {
		slot = &composeSlot{}
		b.slots[key] = slot
	}
	b.mu.Unlock()

	// Held across the reclaim so a concurrent first start of the same slot
	// cannot launch its container while this one is still listing.
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if slot.reclaimed {
		return
	}
	b.removeManagedRun(ctx, taskName, instanceIndex, "")
	// A cancelled ctx may have cut the reclaim short; leave the slot open.
	slot.reclaimed = ctx.Err() == nil
}

// removeManagedRun force-removes the containers this daemon launched for
// (task, instanceIndex). With a runID it removes only that run's container
// (cleanup-on-exit, when `--rm` never fired); with "" it removes every one of
// the slot's containers (reclaim-on-start of a prior life's orphans). The label
// filter, including this daemon's fingerprint, guarantees it only ever removes
// RunWisp's own containers; a genuine name clash with a non-managed container
// still fails loudly at create, which is correct.
func (b *ComposeBackend) removeManagedRun(ctx context.Context, taskName string, instanceIndex int, runID string) {
	ids := b.listManagedContainers(ctx, taskName, instanceIndex, runID)
	if len(ids) == 0 {
		return
	}
	b.removeContainers(ctx, ids)
}

// listManagedContainers returns the IDs of containers carrying this daemon's
// ownership labels for (task, instanceIndex), narrowed to one run when runID is
// set. A docker failure is logged and treated as "none found" — reclaim is
// best-effort and must never block a run.
func (b *ComposeBackend) listManagedContainers(ctx context.Context, taskName string, instanceIndex int, runID string) []string {
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
		return nil
	}
	return parseContainerIDs(out)
}

// removeContainers force-removes the given container IDs in one `docker rm -f`.
func (b *ComposeBackend) removeContainers(ctx context.Context, ids []string) {
	args := append([]string{"rm", "-f"}, ids...)
	if err := exec.CommandContext(ctx, b.dockerCmd, args...).Run(); err != nil {
		slog.Warn("compose: could not remove managed container(s)",
			"ids", strings.Join(ids, ","), "err", err)
	}
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
// line gains a trailing \r, which would corrupt the run log the whole product
// exists to make readable.
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
// arg/option/flag tokens. Split from buildComposeArgs to keep each readable.
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
// container. Falls back to service-only names if the project/service is empty
// (defensive — both should always be set by the time we get here).
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
		"RUNWISP_INSTANCE_INDEX": strconv.Itoa(instanceIndex),
	}
	for k, v := range task.Env {
		merged[k] = v
	}
	for k, v := range task.Secrets {
		merged[k] = v
	}
	var runParams map[string]string
	if run != nil {
		runParams = run.Params
	}
	for k, v := range model.ParamEnvLayer(task.Parameters, runParams) {
		merged[k] = v
	}
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

// LazyComposeBackend defers the docker compose availability probe until first
// use. Mirrors LazyContainerBackend so the daemon boots fast even when the
// docker CLI is slow to respond (or absent).
type LazyComposeBackend struct {
	mu          sync.Mutex
	backend     *ComposeBackend
	fingerprint string
	avail       bool
}

// NewLazyComposeBackend returns a backend that probes `docker compose` on
// first call to Available()/Start(). fingerprint scopes managed-container
// reclaim to this daemon instance.
func NewLazyComposeBackend(fingerprint string) *LazyComposeBackend {
	return &LazyComposeBackend{fingerprint: fingerprint}
}

func (l *LazyComposeBackend) ensureProbed(ctx context.Context) (*ComposeBackend, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.backend == nil {
		l.backend = NewComposeBackend(l.fingerprint)
	}
	// Cache only success. A transient first-probe failure (docker still coming
	// up) must not disable compose for the daemon's lifetime, so re-probe until
	// it reports available — mirroring LazyContainerBackend's retry-on-failure.
	if !l.avail {
		l.avail = l.backend.Available(ctx)
	}
	return l.backend, l.avail
}

func (l *LazyComposeBackend) Available(ctx context.Context) bool {
	_, ok := l.ensureProbed(ctx)
	return ok
}

func (l *LazyComposeBackend) Start(ctx context.Context, task *model.Task, run *model.Run, def model.ExecutionDef) (*Process, error) {
	b, ok := l.ensureProbed(ctx)
	if !ok {
		return nil, fmt.Errorf("docker compose unavailable: install Docker (with the compose plugin) or check that `docker compose version` succeeds")
	}
	return b.Start(ctx, task, run, def)
}
