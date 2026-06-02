// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package demo

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/model"
)

// logLine is one line of captured output bound to a stream (stdout/stderr/system).
type logLine struct {
	text   string
	stream string
}

func out(format string, a ...any) logLine {
	return logLine{fmt.Sprintf(format, a...), logutil.StreamStdout}
}
func errl(format string, a ...any) logLine {
	return logLine{fmt.Sprintf(format, a...), logutil.StreamStderr}
}
func sys(format string, a ...any) logLine {
	return logLine{fmt.Sprintf(format, a...), logutil.StreamSystem}
}

// buildLog produces the full captured output for a run: the task's steady-state
// body, decorated with a tail that reflects how the run ended.
func buildLog(s *runSpec, rng *rand.Rand) []logLine {
	body := successBody(s, rng)
	return decorate(body, s, rng)
}

// successBody returns the lines a successful run of this task would print.
func successBody(s *runSpec, rng *rand.Rand) []logLine {
	t := s.task
	occ := s.occ
	switch t.Name {
	case "backup-postgres":
		return backupBody(occ, rng)
	case "backup-restore-test":
		return restoreBody(occ, rng)
	case "healthcheck-api":
		return healthAPIBody(occ, rng)
	case "healthcheck-tls":
		return healthTLSBody(occ)
	case "rotate-logs":
		return []logLine{
			out("[rotate] scanning /var/log/acme-notes"),
			out("[rotate] compressed %d files, freed %d MB", rng.Intn(20)+6, rng.Intn(400)+120),
			out("[rotate] removed %d archives older than 30 days", rng.Intn(12)+2),
		}
	case "disk-usage-report":
		return diskBody(rng)
	case "deploy-migrate":
		return migrateBody(rng)
	case "weekly-billing-rollup":
		return billingBody(occ, rng)
	case "reindex-search":
		return reindexBody(rng)
	case "import-legacy-data":
		return importBody(rng)
	case "queue-worker":
		return workerBody(s, rng)
	case "cache-warmer":
		return warmerBody(s, rng)
	default:
		return []logLine{
			out("[%s] started at %s", t.Name, occ.UTC().Format(time.RFC3339)),
			out("[%s] working...", t.Name),
			out("[%s] done", t.Name),
		}
	}
}

// decorate appends an ending appropriate to the run's outcome. For failures it
// also truncates the body so the run looks like it died partway through.
func decorate(body []logLine, s *runSpec, rng *rand.Rand) []logLine {
	switch s.outcome.reason {
	case model.ReasonSuccess:
		return body
	case model.ReasonFailed:
		body = head(body, frac(len(body), 0.55, 0.85, rng))
		return append(body, failureTail(s.task, rng)...)
	case model.ReasonTimeout:
		secs := int(s.run.EndAt.Sub(*s.run.StartAt).Seconds())
		return append(body,
			errl("[%s] still running after %ds", s.task.Name, secs),
			sys("run exceeded timeout of %s — sending SIGTERM", s.task.Timeout),
			sys("process did not exit in graceful window — sending SIGKILL"),
		)
	case model.ReasonCrashed:
		body = head(body, frac(len(body), 0.3, 0.6, rng))
		return append(body, crashTail(s.task, rng)...)
	default:
		return body
	}
}

func failureTail(t *model.Task, rng *rand.Rand) []logLine {
	options := [][]logLine{
		{errl("Error: connection refused (after 3 attempts)"), errl("exit status 1")},
		{errl("fatal: command failed with non-zero status"), errl("hint: check upstream availability")},
		{errl("Traceback (most recent call last):"), errl("RuntimeError: unexpected response (502 Bad Gateway)")},
	}
	return options[rng.Intn(len(options))]
}

func crashTail(t *model.Task, rng *rand.Rand) []logLine {
	options := [][]logLine{
		{errl("signal: killed (out of memory)")},
		{errl("panic: runtime error: invalid memory address or nil pointer dereference"), errl("signal SIGSEGV: segmentation violation")},
		{errl("Killed")},
	}
	return options[rng.Intn(len(options))]
}

// --- per-task bodies ---------------------------------------------------------

func backupBody(occ time.Time, rng *rand.Rand) []logLine {
	tables := []string{"public.notes", "public.orgs", "public.users", "public.attachments", "public.audit_log", "public.sessions"}
	lines := []logLine{
		out("[backup] starting snapshot at %s", occ.UTC().Format(time.RFC3339)),
		out("[backup] dumping schema (142 tables, 318 indexes)"),
	}
	for _, tb := range tables {
		lines = append(lines, out("[backup] dumped %s (%d rows)", tb, rng.Intn(900000)+1000))
	}
	size := rng.Intn(220) + 380
	lines = append(lines,
		out("[backup] snapshot complete: %dM compressed in %d.%ds", size, rng.Intn(20)+8, rng.Intn(9)),
		out("[backup] uploaded to s3://acme-notes-backups/%s/", occ.UTC().Format("2006-01-02")),
	)
	return lines
}

func restoreBody(occ time.Time, rng *rand.Rand) []logLine {
	return []logLine{
		out("[restore-test] booting throwaway cluster on :15432"),
		out("[restore-test] restoring %s.dump (%dM)", occ.AddDate(0, 0, -1).UTC().Format("2006-01-02"), rng.Intn(220)+380),
		out("[restore-test] verifying schema fingerprint"),
		out("[restore-test] all 142 tables match production"),
	}
}

func healthAPIBody(occ time.Time, rng *rand.Rand) []logLine {
	zen := []string{
		"Non-blocking is better than blocking.",
		"Design for failure.",
		"Keep it logically awesome.",
		"Responsive is better than fast.",
		"Practicality beats purity.",
	}
	return []logLine{
		out("%s", zen[rng.Intn(len(zen))]),
		out("[health] api ok at %s", occ.UTC().Format("15:04:05Z")),
	}
}

func healthTLSBody(occ time.Time) []logLine {
	exp := occ.AddDate(0, 2, 11)
	return []logLine{
		out("[tls] github.com expires: %s", exp.UTC().Format("Jan 2 15:04:05 2006 GMT")),
	}
}

func diskBody(rng *rand.Rand) []logLine {
	used := rng.Intn(40) + 35
	return []logLine{
		out("Filesystem      Size  Used Avail Use%% Mounted on"),
		out("/dev/sda1       %dG   %dG   %dG  %d%% /", 80, used*80/100, 80-used*80/100, used),
	}
}

func migrateBody(rng *rand.Rand) []logLine {
	head := fmt.Sprintf("%06x", rng.Intn(1<<24))
	n := rng.Intn(4) + 1
	lines := []logLine{
		out("[migrate] HEAD = %s", head),
		out("[migrate] applying %d pending migrations", n),
	}
	names := []string{"add_user_locale", "widen_org_slug", "index_run_created_at", "backfill_note_search", "drop_legacy_tokens"}
	for i := 0; i < n; i++ {
		lines = append(lines, out("[migrate] 00%02d_%s.sql ok", 42+i, names[(rng.Intn(len(names)))]))
	}
	lines = append(lines, out("[migrate] done in %d.%ds", rng.Intn(3)+1, rng.Intn(9)))
	return lines
}

func billingBody(occ time.Time, rng *rand.Rand) []logLine {
	since := occ.AddDate(0, 0, -7)
	return []logLine{
		out("[billing] window: %s → %s", since.UTC().Format("2006-01-02"), occ.UTC().Format("2006-01-02")),
		out("[billing] %d,%03d events processed", rng.Intn(40)+5, rng.Intn(1000)),
		out("[billing] usage_rollup.csv (%d KB) generated", rng.Intn(400)+120),
		out("[billing] report mailed to finance@acme-notes.example"),
	}
}

// reindexBody emits one line per indexed document — tens of thousands of lines,
// the marquee "really long log" of the demo.
func reindexBody(rng *rand.Rand) []logLine {
	n := rng.Intn(32000) + 8000
	lines := make([]logLine, 0, n+4)
	lines = append(lines,
		out("[reindex] dropping search index 'notes_v4'"),
		out("[reindex] streaming documents from primary"),
	)
	for i := 1; i <= n; i++ {
		lines = append(lines, out("[reindex] indexed doc org=%d id=%08d (%d tokens)", i%37+1, i, i%400+20))
	}
	lines = append(lines,
		out("[reindex] committing segment, optimizing"),
		out("[reindex] done: %d docs in %d.%ds", n, rng.Intn(40)+5, rng.Intn(9)),
	)
	return lines
}

// importBody walks several tables row-by-row — the second very-long log.
func importBody(rng *rand.Rand) []logLine {
	lines := []logLine{out("[import] opening legacy_dump.sql (3.2 GB)")}
	for _, table := range []string{"users", "orgs", "notes", "attachments", "audit_log"} {
		rows := rng.Intn(6000) + 2000
		lines = append(lines, out("[import] === table %s ===", table))
		for r := 1; r <= rows; r++ {
			lines = append(lines, out("[import] %s row %06d migrated (rewrote %d columns)", table, r, r%4))
		}
		lines = append(lines, out("[import] %s: %d rows in %d.%ds", table, rows, rng.Intn(6)+1, rng.Intn(9)))
	}
	lines = append(lines,
		out("[import] rebuilding foreign keys"),
		out("[import] complete"),
	)
	return lines
}

// workerBody / warmerBody emit periodic service output proportional to up-time,
// capped so a long-lived instance doesn't write a giant file.
func workerBody(s *runSpec, rng *rand.Rand) []logLine {
	pid := rng.Intn(30000) + 1000
	ticks := serviceTicks(s)
	lines := make([]logLine, 0, ticks+1)
	lines = append(lines, out("[worker %d] starting", pid))
	tm := *s.run.StartAt
	for i := 0; i < ticks; i++ {
		tm = tm.Add(7 * time.Second)
		lines = append(lines, out("[worker %d] processed %d jobs (last: %s)", pid, rng.Intn(8)+1, tm.UTC().Format("15:04:05")))
	}
	return lines
}

func warmerBody(s *runSpec, rng *rand.Rand) []logLine {
	pid := rng.Intn(30000) + 1000
	ticks := serviceTicks(s)
	lines := make([]logLine, 0, ticks+1)
	lines = append(lines, out("[warmer %d] booting", pid))
	tm := *s.run.StartAt
	for i := 0; i < ticks; i++ {
		tm = tm.Add(12 * time.Second)
		lines = append(lines, out("[warmer %d] refreshed %d keys (%s)", pid, rng.Intn(20)+4, tm.UTC().Format("15:04:05Z")))
	}
	return lines
}

// serviceTicks scales the number of periodic lines to the run's up-time, capped.
func serviceTicks(s *runSpec) int {
	secs := int(s.run.EndAt.Sub(*s.run.StartAt).Seconds())
	ticks := secs / 8
	if ticks > 1200 {
		ticks = 1200
	}
	if ticks < 1 {
		ticks = 1
	}
	return ticks
}

// --- small helpers -----------------------------------------------------------

// head returns the first n lines (clamped).
func head(lines []logLine, n int) []logLine {
	if n < 1 {
		n = 1
	}
	if n > len(lines) {
		n = len(lines)
	}
	return lines[:n]
}

// frac returns a count between lo and hi fractions of total.
func frac(total int, lo, hi float64, rng *rand.Rand) int {
	if total <= 1 {
		return total
	}
	f := lo + rng.Float64()*(hi-lo)
	return int(float64(total) * f)
}
