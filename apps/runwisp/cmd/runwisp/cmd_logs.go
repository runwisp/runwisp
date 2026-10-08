// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/charmbracelet/colorprofile"
	"github.com/runwisp/runwisp/apps/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/apps/runwisp/internal/events"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/runlog"
	"github.com/runwisp/runwisp/apps/runwisp/internal/server"
	"github.com/spf13/cobra"
)

var logsFlags struct {
	Follow bool
	Lines  string
	JSON   bool
}

// logsCmd prints or follows the captured output of tasks, services, and runs.
// It is read-only: targets use the stop/start grammar, but a glob also matches
// manual_trigger = false entries, since reading a log isn't control.
var logsCmd = &cobra.Command{
	Use:   "logs <target...>",
	Short: "Print or follow the output of tasks, services, and runs",
	Long: `Prints the captured output of one or more tasks, services, or runs from a
running daemon, then exits.

A target is a task name, a service name, a run ID, or a quoted shell-style
glob matched against task and service names ('web*', or '*' for everything).
A run ID selects that run. A name selects its active runs, or its most recent
finished run when none is active, so a crashed service still shows why it
died.

Each run's full log is printed by default. -n N prints only its last N lines,
-n +N only its first N. Lines keep their stream: what the task wrote to stderr
goes to stderr, so use 2>&1 to grep both. When the output mixes several tasks,
or several instances of one service, each line is prefixed with its source
('web#2 | ...'). How each finished run ended is reported on stderr.

With -f, runwisp logs keeps streaming new output, starting from the last 10
lines of each run. With a name or glob it also follows every new run of the
matching tasks, including tasks a later 'runwisp reload' adds, until you stop
it with Ctrl+C. With only run IDs it exits once those runs end.

With --json, stdout carries one JSON object per line: {"type":"line", ...} for
each log line, and {"type":"end", ...} with the run's outcome (the same fields
as 'runwisp run --json') when a run ends.

The exit code reports whether the logs could be shown, not how the runs ended;
'runwisp run' is the command that exits with a task's exit code.

With --url (or RUNWISP_URL), logs come from a remote daemon instead, with the
same CHAP login and session caching as 'runwisp run --url'.`,
	Example: `  runwisp logs backup
  runwisp logs backup -n 50 | grep -i error
  runwisp logs -f web worker
  runwisp logs -f '*'
  runwisp logs 01J8Z3K9QK6VN8XG2R5F7T1C4M -n +20
  runwisp logs -f --json 'batch-*' | jq -r 'select(.type == "line") | .text'`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		opts, err := parseLogsOptions(logsFlags.Follow, logsFlags.Lines, logsFlags.JSON)
		if err != nil {
			return err
		}
		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return runLogs(ctx, cmd.OutOrStdout(), cmd.ErrOrStderr(), flags, controlRemote, args, opts)
	},
}

func init() {
	logsCmd.Flags().BoolVarP(&logsFlags.Follow, "follow", "f", false, "keep streaming new output; with a name or glob, also follow new runs")
	logsCmd.Flags().StringVarP(&logsFlags.Lines, "lines", "n", "", "print the last N lines of each run, or the first N with +N (default: all; 10 with --follow)")
	logsCmd.Flags().BoolVar(&logsFlags.JSON, "json", false, "print one JSON object per line: log lines and run outcomes")
	addRemoteFlags(logsCmd)
}

// followTailLines is how much backlog -f replays per run when -n isn't given.
const followTailLines = 10

// logsOptions is the parsed flag set. Lines < 0 means every line; Head picks
// the first Lines lines instead of the last.
type logsOptions struct {
	Follow bool
	Head   bool
	Lines  int64
	JSON   bool
}

// parseLogsOptions validates -n: "N" is a tail, "+N" a head. The flag is a
// string because an int flag would read "+20" as 20 and lose the head.
func parseLogsOptions(follow bool, lines string, jsonOut bool) (logsOptions, error) {
	opts := logsOptions{Follow: follow, Lines: -1, JSON: jsonOut}
	if lines == "" {
		if follow {
			opts.Lines = followTailLines
		}
		return opts, nil
	}
	digits, head := strings.CutPrefix(lines, "+")
	n, err := strconv.ParseUint(digits, 10, 63)
	if err != nil {
		return logsOptions{}, fmt.Errorf("invalid --lines %q: use N for the last N lines or +N for the first N", lines)
	}
	if head && follow {
		return logsOptions{}, errors.New("--lines +N (the first lines) can't be combined with --follow")
	}
	opts.Head, opts.Lines = head, int64(n)
	return opts, nil
}

// runLogs resolves the targets, selects their runs, and prints or follows them.
func runLogs(ctx context.Context, out, errOut io.Writer, f Flags, rf remoteFlags, args []string, opts logsOptions) error {
	client, baseURL, tasks, err := connectAndListTasks(ctx, f, rf)
	if err != nil {
		return err
	}
	targets, runIDs, err := resolveTargets(args, tasks, true, allTargets)
	if err != nil {
		return err
	}

	// Subscribe before selecting runs, so a run starting in between is seen
	// either in the listing or as an event; the follower dedupes by ID.
	var events <-chan apiclient.RunStreamEvent
	if opts.Follow && len(targets) > 0 {
		if events, err = client.StreamRunEvents(ctx, ""); err != nil {
			return fmt.Errorf("open event stream: %w", err)
		}
	}

	runs, err := selectRuns(ctx, client, baseURL, targets, runIDs, true)
	if err != nil {
		return err
	}
	sink := newLogSink(out, errOut, opts.JSON, tasks, targets, runIDs, runs)
	if opts.Follow {
		return followLogs(ctx, client, sink, runs, nil, events, livePatterns(args, runIDs), opts)
	}
	return printSnapshots(ctx, client, sink, runs, opts)
}

// logAttach is what a control verb's --attach sets up before it dispatches:
// the event stream is open and the targets' active runs are known, so a run
// the verb starts can only show up as an event, and is streamed from its
// first line.
type logAttach struct {
	client *apiclient.Client
	tasks  []model.TaskResponse
	events <-chan apiclient.RunStreamEvent
	runs   []*model.Run
}

// openLogAttach subscribes to run events, then lists what is already running.
// A finished run is left out: it's not what the verb acted on.
//
// ponytail: nothing reads the event stream while the verb dispatches, so its
// events wait in the client channel and socket buffers. Only a dispatch long
// enough to fill those (a restart draining for minutes) makes the daemon drop
// the stream; reading it in a goroutine from here is the fix if that bites.
func openLogAttach(ctx context.Context, client *apiclient.Client, baseURL string, tasks, targets []model.TaskResponse, runIDs []string) (*logAttach, error) {
	a := &logAttach{client: client, tasks: tasks}
	var err error
	if len(targets) > 0 {
		if a.events, err = client.StreamRunEvents(ctx, ""); err != nil {
			return nil, fmt.Errorf("open event stream: %w", err)
		}
	}
	if a.runs, err = selectRuns(ctx, client, baseURL, targets, runIDs, false); err != nil {
		return nil, err
	}
	return a, nil
}

// follow streams the runs of the targets the verb succeeded on, as `logs -f`
// does, matching new runs by exact task name. With replaced, the runs listed
// before dispatch are the ones the verb ended, so they are skipped rather than
// followed.
func (a *logAttach) follow(ctx context.Context, out, errOut io.Writer, targets []model.TaskResponse, runIDs []string, replaced bool) error {
	names := make([]string, len(targets))
	for i, t := range targets {
		names[i] = t.Name
	}
	runs := slices.DeleteFunc(a.runs, func(r *model.Run) bool {
		return !slices.Contains(names, r.TaskName) && !slices.Contains(runIDs, r.ID)
	})
	var skip []*model.Run
	if replaced {
		skip, runs = runs, nil
	}
	events := a.events
	if len(names) == 0 {
		events = nil // only run IDs succeeded: exit once they end
	}
	sink := newLogSink(out, errOut, false, a.tasks, targets, runIDs, runs)
	return followLogs(ctx, a.client, sink, runs, skip, events, names, logsOptions{Follow: true, Lines: followTailLines})
}

// selectRuns picks the runs the targets name: each task's, then each run ID's.
// lastEnded is selectTaskRuns'.
func selectRuns(ctx context.Context, client *apiclient.Client, baseURL string, targets []model.TaskResponse, runIDs []string, lastEnded bool) ([]*model.Run, error) {
	runs, err := selectTaskRuns(ctx, client, targets, lastEnded)
	if err != nil {
		return nil, err
	}
	for _, id := range runIDs {
		run, err := client.GetRun(ctx, id)
		if err != nil {
			return nil, controlError(err, baseURL, "read", id, true)
		}
		runs = append(runs, run)
	}
	return runs, nil
}

// selectTaskRuns gives each task or service its active runs, oldest first, or
// else, with lastEnded, its most recent finished run. A task that has never
// run is reported and skipped.
func selectTaskRuns(ctx context.Context, client *apiclient.Client, targets []model.TaskResponse, lastEnded bool) ([]*model.Run, error) {
	if len(targets) == 0 {
		return nil, nil
	}
	active, _, err := client.ListRuns(ctx, apiclient.RunsParams{Status: string(model.PhaseRunning), Limit: 1000})
	if err != nil {
		return nil, fmt.Errorf("list active runs: %w", err)
	}
	// ULIDs sort chronologically, so this puts every task's runs oldest first.
	slices.SortFunc(active, func(a, b model.Run) int { return strings.Compare(a.ID, b.ID) })

	var runs []*model.Run
	for _, t := range targets {
		mine := runsOf(active, t.Name)
		if len(mine) == 0 && lastEnded {
			last, _, err := client.ListRuns(ctx, apiclient.RunsParams{TaskName: t.Name, Status: string(model.PhaseEnded), Limit: 1})
			if err != nil {
				return nil, fmt.Errorf("list runs of %q: %w", t.Name, err)
			}
			if len(last) == 0 {
				slog.Info("No runs yet", "task", t.Name)
				continue
			}
			mine = []*model.Run{&last[0]}
		}
		runs = append(runs, mine...)
	}
	return runs, nil
}

func runsOf(runs []model.Run, taskName string) []*model.Run {
	var out []*model.Run
	for i := range runs {
		if runs[i].TaskName == taskName {
			out = append(out, &runs[i])
		}
	}
	return out
}

// printSnapshots prints each run's log in turn; an interrupt just stops early.
func printSnapshots(ctx context.Context, client *apiclient.Client, sink *logSink, runs []*model.Run, opts logsOptions) error {
	for _, run := range runs {
		if err := printRunLog(ctx, client, sink, run, opts); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
	}
	return nil
}

// printRunLog prints a snapshot of run's log as opts selects it, then how the
// run ended if it has. A run still going shows its output so far.
func printRunLog(ctx context.Context, client *apiclient.Client, sink *logSink, run *model.Run, opts logsOptions) error {
	if opts.Lines != 0 {
		if err := printLogLines(ctx, client, sink, run, opts); err != nil {
			return fmt.Errorf("read log of run %s: %w", run.ID, err)
		}
	}
	if run.Status == model.PhaseEnded {
		sink.end(run)
	}
	return nil
}

// printLogLines pages through the selected lines of run's log, up to the line
// count the first page saw, so a run still writing can't keep it going.
func printLogLines(ctx context.Context, client *apiclient.Client, sink *logSink, run *model.Run, opts logsOptions) error {
	from, limit := opts.firstPage()
	page, err := client.GetLogPage(ctx, run.ID, from, limit)
	if err != nil {
		return err
	}
	start, end := opts.window(page)
	if page.FirstAvailable > start {
		slog.Warn("Earlier log lines were rotated away; raise log_max_size to keep more",
			"task", run.TaskName, "run", run.ID, "first_available_line", page.FirstAvailable)
	}
	for len(page.Lines) > 0 {
		next := sink.lines(run, page.Lines, end)
		if next >= end {
			return nil
		}
		if page, err = client.GetLogPage(ctx, run.ID, next, min(end-next, server.LogPageMaxLimit)); err != nil {
			return err
		}
	}
	return nil
}

func (o logsOptions) tail() bool { return !o.Head && o.Lines > 0 }

// firstPage is the first log page to request for the selected lines.
func (o logsOptions) firstPage() (from, limit int64) {
	switch {
	case o.tail():
		return -o.Lines, server.LogPageMaxLimit
	case o.Head:
		return 0, min(o.Lines, server.LogPageMaxLimit)
	}
	return 0, server.LogPageMaxLimit
}

// window is the line range [start, end) to print, from the first page's
// totals. start is where the selection begins before rotation clamps it, so a
// caller can tell whether rotation cut into it.
func (o logsOptions) window(page server.LogPageBody) (start, end int64) {
	end = page.TotalLines
	switch {
	case o.tail():
		start = max(end-o.Lines, 0)
	case o.Head:
		end = min(end, page.FirstAvailable+o.Lines)
	}
	return start, end
}

// livePatterns are the args a new run's task name is matched against while
// following: every name and glob (so a task a later reload adds still matches
// a glob), minus the run IDs.
func livePatterns(args, runIDs []string) []string {
	return slices.DeleteFunc(slices.Clone(args), func(a string) bool { return slices.Contains(runIDs, a) })
}

func matchesAny(patterns []string, name string) bool {
	for _, p := range patterns {
		if ok, _ := path.Match(p, name); ok {
			return true
		}
	}
	return false
}

// followLogs streams every selected run, plus — when events is set — every new
// run of a matching task, until ctx is cancelled. With no events (run IDs only)
// it returns once all the runs have ended. Events about the skip runs are
// ignored.
//
// ponytail: one SSE stream per followed run. A remote daemon allows 16 streams
// per client IP, so -f over --url tops out around 15 live runs; a server-side
// multiplexed log stream is the upgrade if that ever matters.
func followLogs(ctx context.Context, client *apiclient.Client, sink *logSink, runs, skip []*model.Run, events <-chan apiclient.RunStreamEvent, patterns []string, opts logsOptions) error {
	ctx, cancel := context.WithCancel(ctx)
	fl := &logFollower{ctx: ctx, cancel: cancel, client: client, sink: sink, patterns: patterns,
		results: make(chan error), tracked: map[string]bool{}}
	for _, r := range skip {
		fl.tracked[r.ID] = true
	}

	if err := fl.start(runs, opts); err != nil {
		return fl.stop(err)
	}
	for events != nil || fl.active > 0 {
		select {
		case <-ctx.Done():
			return fl.stop(nil)
		case err := <-fl.results:
			fl.active--
			if err != nil {
				return fl.stop(err)
			}
		case ev, ok := <-events:
			if !ok {
				if ctx.Err() != nil {
					return fl.stop(nil)
				}
				return fl.stop(errors.New("lost the connection to the daemon"))
			}
			fl.onEvent(ev)
		}
	}
	return fl.stop(nil)
}

// logFollower is the state of one `logs -f`. Only followLogs' goroutine
// touches it; each run's stream goroutine reports back on results.
type logFollower struct {
	ctx      context.Context
	cancel   context.CancelFunc
	client   *apiclient.Client
	sink     *logSink
	patterns []string
	results  chan error
	// ponytail: tracked keeps every run ID seen for the life of the follow, so
	// a late event never reports a run twice. ~50 bytes a run: a minutely cron
	// adds ~2 MB a month. Prune ended runs after a grace period if that matters.
	tracked map[string]bool
	active  int
}

// start streams each selected run from its backlog. An ended run gets its tail
// and outcome printed instead, as nothing more will be written to it.
func (fl *logFollower) start(runs []*model.Run, opts logsOptions) error {
	for _, run := range runs {
		if run.Status == model.PhaseEnded {
			fl.tracked[run.ID] = true
			if err := printRunLog(fl.ctx, fl.client, fl.sink, run, opts); err != nil {
				return err
			}
			continue
		}
		from, err := backlogStart(fl.ctx, fl.client, run, opts.Lines)
		if err != nil {
			return err
		}
		fl.follow(run, from)
	}
	return nil
}

func (fl *logFollower) follow(run *model.Run, from int64) {
	fl.tracked[run.ID] = true
	fl.active++
	go func() { fl.results <- followOneRun(fl.ctx, fl.client, fl.sink, run, from) }()
}

// onEvent follows a new run of a matching task from its first line; a terminal
// event for a started run we missed replays its log off disk and then ends. A
// run that ended without ever starting (missed, skipped, queue_full…) has no
// output, only an outcome worth seeing.
func (fl *logFollower) onEvent(ev apiclient.RunStreamEvent) {
	run := fl.newRun(ev)
	switch {
	case run == nil:
	case ev.Type == string(events.EventRunStarted) || run.StartedAt != nil:
		fl.follow(run, 0)
	default:
		fl.tracked[run.ID] = true
		fl.sink.end(run)
	}
}

// stop cancels every stream and waits for them (cancelled, they return
// promptly), so none is left writing after followLogs returns.
func (fl *logFollower) stop(err error) error {
	fl.cancel()
	for ; fl.active > 0; fl.active-- {
		<-fl.results
	}
	return err
}

// newRun returns the run an app-stream event announces when it is new to this
// follow and belongs to a matching task: started, or ended without our having
// streamed it. Anything else is nil.
func (fl *logFollower) newRun(ev apiclient.RunStreamEvent) *model.Run {
	switch ev.Type {
	case string(events.EventRunStarted), string(events.EventRunCompleted), string(events.EventRunFailed):
	default:
		return nil
	}
	var body server.RunEventBody
	if err := json.Unmarshal(ev.Data, &body); err != nil || body.Run == nil {
		return nil
	}
	if fl.tracked[body.Run.ID] || !matchesAny(fl.patterns, body.Run.TaskName) {
		return nil
	}
	return body.Run
}

// backlogStart is the line a followed run's stream starts at so it replays its
// last n lines. n == 0 means none: start at the current end of the log.
func backlogStart(ctx context.Context, client *apiclient.Client, run *model.Run, n int64) (int64, error) {
	if n > 0 {
		return -n, nil
	}
	page, err := client.GetLogPage(ctx, run.ID, -1, 1)
	if err != nil {
		return 0, fmt.Errorf("read log of run %s: %w", run.ID, err)
	}
	return page.TotalLines, nil
}

// followOneRun streams run's log to sink from line `from` and, once it ends,
// reports its outcome. It returns nil when ctx is cancelled.
func followOneRun(ctx context.Context, client *apiclient.Client, sink *logSink, run *model.Run, from int64) error {
	done, err := followRunLog(ctx, client, run.TaskName, run.ID, from, followQuietAfter, func(l server.LogLineEntry) {
		sink.line(run, l)
	})
	switch {
	case errors.Is(err, errLogStreamStalled):
		return fmt.Errorf("could not open the log stream of run %s: the daemon kept closing it", run.ID)
	case err != nil:
		return fmt.Errorf("follow run %s: %w", run.ID, err)
	case !done:
		return nil
	}
	final, err := fetchTerminalRun(ctx, client, run.TaskName, run.ID)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("fetch final state of run %s: %w", run.ID, err)
	}
	sink.end(final)
	return nil
}

// logSink is where every printed line and outcome goes. Its mutex keeps lines
// from concurrently followed runs whole, and it decides the per-line prefix.
type logSink struct {
	mu     sync.Mutex
	out    io.Writer
	errOut io.Writer
	enc    *json.Encoder // nil in text mode

	// prefix is on when the output can mix sources: more than one target, or
	// a service running several instances. It depends on the targets and the
	// config only, never on which runs happen to be live, so a script sees
	// the same shape every time.
	prefix bool
	width  int
	color  bool
	colors map[string]string
	multi  map[string]int // instance count of each service with more than one
}

func newLogSink(out, errOut io.Writer, jsonOut bool, tasks, targets []model.TaskResponse, runIDs []string, runs []*model.Run) *logSink {
	s := &logSink{out: out, errOut: errOut, colors: map[string]string{}, multi: map[string]int{}}
	if jsonOut {
		s.enc = json.NewEncoder(out)
		s.enc.SetEscapeHTML(false)
		return s
	}
	for _, t := range tasks {
		if t.Kind.IsService() && t.Instances > 1 {
			s.multi[t.Name] = t.Instances
		}
	}
	s.prefix = len(targets)+len(runIDs) > 1
	for _, t := range targets {
		if s.multi[t.Name] > 0 {
			s.prefix = true
		}
		s.width = max(s.width, len(model.InstanceLabel(t.Name, t.Instances-1, s.multi[t.Name])))
	}
	for _, r := range runs {
		s.width = max(s.width, len(s.label(r)))
	}
	s.color = colorprofile.Detect(out, os.Environ()) >= colorprofile.ANSI
	return s
}

// logLineRecord is one --json log line: the API's line, tagged with its run.
type logLineRecord struct {
	Type  string `json:"type"`
	Task  string `json:"task"`
	RunID string `json:"runId"`
	server.LogLineEntry
}

// logEndRecord is the --json outcome of a run: the `run --json` document,
// tagged so it can share the stream with log lines.
type logEndRecord struct {
	Type string `json:"type"`
	runJSONDoc
}

func (s *logSink) line(run *model.Run, l server.LogLineEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.enc != nil {
		_ = s.enc.Encode(logLineRecord{Type: "line", Task: run.TaskName, RunID: run.ID, LogLineEntry: l})
		return
	}
	writeLogLine(s.out, s.errOut, l.Stream, l.Text, s.linePrefix(run))
}

// lines prints a page's lines below end and returns the next line to fetch.
func (s *logSink) lines(run *model.Run, lines []server.LogLineEntry, end int64) int64 {
	for _, l := range lines {
		if l.N >= end {
			return end
		}
		s.line(run, l)
	}
	return lines[len(lines)-1].N + 1
}

func (s *logSink) end(run *model.Run) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.enc != nil {
		_ = s.enc.Encode(logEndRecord{Type: "end", runJSONDoc: newExecJSONDoc(run.TaskName, run)})
		return
	}
	runlog.LogEnded(run)
}

func (s *logSink) label(run *model.Run) string {
	return model.InstanceLabel(run.TaskName, run.InstanceIndex, s.multi[run.TaskName])
}

// logPrefixColors cycles through distinguishable ANSI colors, skipping red,
// which the CLI keeps for errors.
var logPrefixColors = []string{"36", "33", "32", "35", "34", "96", "93", "92", "95", "94"}

func (s *logSink) linePrefix(run *model.Run) string {
	if !s.prefix {
		return ""
	}
	p := padTo(s.label(run), s.width) + " | "
	if !s.color {
		return p
	}
	c, ok := s.colors[run.TaskName]
	if !ok {
		c = logPrefixColors[len(s.colors)%len(logPrefixColors)]
		s.colors[run.TaskName] = c
	}
	return "\x1b[" + c + "m" + p + "\x1b[0m"
}
