// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package importer

import (
	"encoding/json"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/runwisp/runwisp/apps/runwisp/internal/composespec"
	"github.com/runwisp/runwisp/apps/runwisp/internal/cronspec"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
)

// Ofelia (github.com/mcuadros/ofelia) schedules jobs in Docker setups. Its jobs
// come from an INI file (`[job-exec "name"]`) or from labels on the services of a
// compose file (`ofelia.job-exec.name.schedule`). Both map onto [tasks.*]:
//
//	job-exec  → a command in a running container (compose exec, or `docker exec`)
//	job-run   → `docker run --rm` of an image
//	job-local → a command on the host
//
// job-service-run (Swarm) and job-compose have no RunWisp counterpart and come
// out as a report row with no TOML, never as a silent drop.

// OfeliaOptions tunes Ofelia parsing.
type OfeliaOptions struct {
	// Existing carries the entries the live config already owns, see Owned.
	Existing Owned
}

// ParseOfeliaFiles converts Ofelia jobs from one or more files. A .yml/.yaml
// file is read as a compose file and its services' ofelia.* labels are the jobs;
// anything else is read as Ofelia's INI config.
func ParseOfeliaFiles(paths []string, opts OfeliaOptions) (*Result, error) {
	st := newOfeliaState(opts)
	for _, p := range paths {
		if ext := strings.ToLower(filepath.Ext(p)); ext == ".yml" || ext == ".yaml" {
			if err := st.loadCompose(p); err != nil {
				return nil, err
			}
			continue
		}
		f, err := os.Open(p)
		if err != nil {
			return nil, err
		}
		err = st.loadINI(f)
		f.Close()
		if err != nil {
			return nil, err
		}
	}
	return st.finish(), nil
}

// ParseOfeliaReader converts an Ofelia INI config read from r.
func ParseOfeliaReader(r io.Reader, opts OfeliaOptions) (*Result, error) {
	st := newOfeliaState(opts)
	if err := st.loadINI(r); err != nil {
		return nil, err
	}
	return st.finish(), nil
}

// ofeliaJob is one job as the source described it, before mapping.
type ofeliaJob struct {
	kind string // job-exec, job-run, job-local, ...
	name string
	// source is what the report row calls it: the INI section or the label owner.
	source string
	// service and composeFile are set for a job read from compose labels, where
	// a job-exec targets that service.
	service, composeFile string
	keys                 map[string][]string
}

// one returns the key's last value, "" when absent.
func (j *ofeliaJob) one(key string) string {
	if v := j.keys[key]; len(v) > 0 {
		return strings.TrimSpace(v[len(v)-1])
	}
	return ""
}

// list returns every value of a key. A compose label holds one string, so a
// JSON array in it (`["a:/b","c:/d"]`) is a list, the way Ofelia reads it.
func (j *ofeliaJob) list(key string) []string {
	var out []string
	for _, v := range j.keys[key] {
		var arr []string
		if strings.HasPrefix(strings.TrimSpace(v), "[") && json.Unmarshal([]byte(v), &arr) == nil {
			out = append(out, arr...)
		} else if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

type ofeliaState struct {
	res   *Result
	names *namer
	jobs  []*ofeliaJob
	// byID finds a job a later label or section adds keys to.
	byID map[string]*ofeliaJob
}

func newOfeliaState(opts OfeliaOptions) *ofeliaState {
	res := &Result{}
	return &ofeliaState{res: res, names: newNamer(res, opts.Existing, ""), byID: map[string]*ofeliaJob{}}
}

func (st *ofeliaState) job(kind, name, source, service, composeFile string) *ofeliaJob {
	id := source + "\x00" + kind + "\x00" + name
	if j, ok := st.byID[id]; ok {
		return j
	}
	j := &ofeliaJob{kind: kind, name: name, source: source, service: service, composeFile: composeFile, keys: map[string][]string{}}
	st.byID[id] = j
	st.jobs = append(st.jobs, j)
	return j
}

// ofeliaSection matches `job-exec "name"`, with or without the quotes.
var ofeliaSection = regexp.MustCompile(`^(job-[a-z-]+)\s+"?([^"]+?)"?$`)

func (st *ofeliaState) loadINI(r io.Reader) error {
	sections, err := parseINI(r)
	if err != nil {
		return err
	}
	for i := range sections {
		s := &sections[i]
		m := ofeliaSection.FindStringSubmatch(s.name)
		if m == nil {
			st.res.fileNote(NoteSectionDaemon,
				"["+s.name+"] is Ofelia's own configuration (logging, notifications, Docker access). "+
					"RunWisp has no equivalent section; notifications are [notifiers.*] and routes, see the docs.")
			continue
		}
		j := st.job(m[1], m[2], "["+s.name+"]", "", "")
		for _, k := range s.keys {
			j.keys[k] = append(j.keys[k], s.all[k]...)
		}
	}
	return nil
}

func (st *ofeliaState) loadCompose(path string) error {
	// Absolute, so the emitted compose_file means the same wherever the config is
	// loaded, and the import's own validation can find it.
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	project, err := composespec.Load(path, []string{"*"}, nil)
	if err != nil {
		return err
	}
	var globals []string
	for _, svc := range project.Services {
		globals = append(globals, st.readServiceLabels(svc, path)...)
	}
	if len(globals) > 0 {
		st.res.fileNote(NoteSectionDaemon,
			"Ofelia settings in labels ("+strings.Join(globals, ", ")+") have no RunWisp equivalent; "+
				"notifications are [notifiers.*] and routes, see the docs.")
	}
	return nil
}

// readServiceLabels collects one service's ofelia.job-* labels into jobs and
// returns its other ofelia.* labels, which have no RunWisp equivalent.
func (st *ofeliaState) readServiceLabels(svc composespec.Service, path string) (globals []string) {
	jobs := 0
	for _, key := range slices.Sorted(maps.Keys(svc.Labels)) {
		rest, ok := strings.CutPrefix(key, "ofelia.")
		if !ok {
			continue
		}
		kind, jobKey, ok := strings.Cut(rest, ".")
		if !ok || !strings.HasPrefix(kind, "job-") {
			if rest != "enabled" && rest != "service" {
				globals = append(globals, key)
			}
			continue
		}
		dot := strings.LastIndex(jobKey, ".")
		if dot < 0 {
			continue
		}
		jobs++
		if !ofeliaEnabled(svc.Labels) {
			continue
		}
		j := st.job(kind, jobKey[:dot], svc.Name+": ofelia."+kind+"."+jobKey[:dot], svc.Name, path)
		j.keys[jobKey[dot+1:]] = append(j.keys[jobKey[dot+1:]], svc.Labels[key])
	}
	if jobs > 0 && !ofeliaEnabled(svc.Labels) {
		st.res.fileNote(NoteSectionUnrecognized,
			"service "+svc.Name+" has ofelia.job-* labels but no ofelia.enabled=true; Ofelia ignores "+
				"those jobs, so they weren't imported.")
	}
	return globals
}

// ofeliaEnabled reports whether Ofelia would read a service's job labels:
// ofelia.enabled (containers) or ofelia.service (Ofelia's own container).
func ofeliaEnabled(labels map[string]string) bool {
	for _, k := range [...]string{"ofelia.enabled", "ofelia.service"} {
		if on, _ := parseBool(labels[k]); on {
			return true
		}
	}
	return false
}

func (st *ofeliaState) finish() *Result {
	for _, j := range st.jobs {
		st.process(j)
	}
	return st.res
}

func (st *ofeliaState) process(j *ofeliaJob) {
	switch j.kind {
	case "job-exec", "job-run", "job-local":
	default:
		st.res.addItemAt(j.source, 0).note(NoteSectionUnsupported,
			"["+j.kind+"] isn't supported: RunWisp has no Swarm services or compose-up jobs. Nothing was imported for it.")
		return
	}

	run, env, dropped := ofeliaCommand(j)
	id := OwnedEntry{Kind: model.KindTask, Run: run}
	ref, name, skip := st.names.resolve(j.source, finalizeTaskName(j.name, "job"), id, 0)
	if skip {
		return
	}

	b := block{header: "tasks." + name}
	schedule := j.one("schedule")
	switch err := cronspec.Validate(schedule, ""); {
	case schedule == "":
		b.setComment("cron", `""`, "TODO: this job had no schedule.")
		ref.note(NoteCronUnparseable, "no schedule given.")
	case err != nil:
		b.setComment("cron", tomlString(schedule), "TODO: RunWisp couldn't parse this schedule, fix it.")
		ref.note(NoteCronUnparseable, "schedule "+schedule+" didn't parse: "+err.Error())
	default:
		b.set("cron", tomlString(schedule))
	}

	if j.service != "" && j.kind == "job-exec" {
		b.set("compose_file", tomlString(j.composeFile))
		b.set("compose_service", tomlString(j.service))
	}
	// job-run may leave the command to the image; the others have nothing to run.
	if j.one("command") == "" && j.kind != "job-run" {
		b.setComment("run", tomlVerbatimString(run), "TODO: this job had no command.")
		ref.note(NoteNoCommand, "no command given.")
	} else {
		b.set("run", tomlVerbatimString(run))
	}
	if j.kind == "job-local" {
		if dir := j.one("dir"); dir != "" {
			b.set("working_dir", tomlString(dir))
		}
	}
	// Ofelia starts another copy while the last one still runs unless told not to;
	// RunWisp queues it, and `skip` is the closest match for no-overlap.
	if on, _ := parseBool(j.one("no-overlap")); on {
		b.set("on_overlap", tomlString(string(model.PolicySkip)))
	}

	if len(dropped) > 0 {
		ref.note(NoteKeysUnsupported, "no RunWisp equivalent for: "+strings.Join(dropped, ", ")+". Left out.")
	}
	blocks := []block{b}
	if eb, ok := envBlock(b.header+".env", env); ok {
		blocks = append(blocks, eb)
	}
	ref.emit(name, model.KindTask, schedule, run, blocks...)
}

// ofeliaCommand builds the task's `run`, the environment it needs as a table
// (so values stay off argv) and the keys that have no RunWisp equivalent.
func ofeliaCommand(j *ofeliaJob) (run string, env map[string]string, dropped []string) {
	command := j.one("command")
	env = ofeliaEnv(j.list("environment"))
	handled := map[string]bool{"schedule": true, "command": true, "no-overlap": true, "environment": true}
	mark := func(keys ...string) {
		for _, k := range keys {
			handled[k] = true
		}
	}

	switch {
	case j.kind == "job-local":
		mark("dir")
		run = command
	case j.kind == "job-exec" && j.service != "":
		// compose exec: the stack's own project, no container name to keep in step.
		run = command
	case j.kind == "job-exec":
		mark("container", "user", "working-dir")
		run = joinCommand(ofeliaExecArgs(j, env), command)
	case j.kind == "job-run":
		mark("image", "user", "working-dir", "network", "hostname", "volume", "delete", "pull")
		run = joinCommand(ofeliaRunArgs(j, env), command)
	}

	// tty = false is Ofelia's default and RunWisp runs without one, so only an
	// enabled tty is a difference worth reporting.
	if v := j.one("tty"); v != "" && !isTrue(v) {
		mark("tty")
	}
	for k := range j.keys {
		if !handled[k] {
			dropped = append(dropped, k)
		}
	}
	slices.Sort(dropped)
	return run, env, dropped
}

func ofeliaExecArgs(j *ofeliaJob, env map[string]string) []string {
	args := []string{"docker", "exec"}
	args = ofeliaFlag(args, "-u", j.one("user"))
	args = ofeliaFlag(args, "-w", j.one("working-dir"))
	args = ofeliaEnvFlags(args, env)
	return append(args, shellWord(j.one("container")))
}

func ofeliaRunArgs(j *ofeliaJob, env map[string]string) []string {
	args := []string{"docker", "run"}
	if del := j.one("delete"); del == "" || isTrue(del) {
		args = append(args, "--rm")
	}
	args = append(args, "--init")
	args = ofeliaFlag(args, "-u", j.one("user"))
	args = ofeliaFlag(args, "-w", j.one("working-dir"))
	args = ofeliaFlag(args, "--network", j.one("network"))
	args = ofeliaFlag(args, "--hostname", j.one("hostname"))
	if isTrue(j.one("pull")) {
		args = append(args, "--pull=always")
	}
	for _, v := range j.list("volume") {
		args = ofeliaFlag(args, "-v", v)
	}
	args = ofeliaEnvFlags(args, env)
	return append(args, shellWord(j.one("image")))
}

// joinCommand puts the job's command after the docker arguments. Ofelia splits
// it into words itself; here it reaches `sh -c` as written, which reads the same
// for any command without shell metacharacters.
func joinCommand(args []string, command string) string {
	if command == "" {
		return strings.Join(args, " ")
	}
	return strings.Join(args, " ") + " " + command
}

func ofeliaFlag(args []string, flag, value string) []string {
	if value == "" {
		return args
	}
	return append(args, flag, shellWord(value))
}

// ofeliaEnvFlags passes each variable by name: its value comes from the task's
// [tasks.x.env] table, so it never appears on the command line.
func ofeliaEnvFlags(args []string, env map[string]string) []string {
	for _, k := range slices.Sorted(maps.Keys(env)) {
		args = append(args, "-e", k)
	}
	return args
}

// ofeliaEnv reads `KEY=value` entries; an entry with no `=` has no value to copy.
func ofeliaEnv(entries []string) map[string]string {
	env := map[string]string{}
	for _, e := range entries {
		if k, v, ok := strings.Cut(e, "="); ok && k != "" {
			env[k] = v
		}
	}
	return env
}

func isTrue(s string) bool { on, _ := parseBool(s); return on }
