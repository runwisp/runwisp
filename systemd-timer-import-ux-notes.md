# systemd timer import: real-VM UX test notes

Date 2026-11-10. Build: v1.9.2 from `main`, linux/arm64, with a placeholder UI.
VM: multipass Ubuntu 24.04 (stock 17 timers) plus custom units.

Journey tested:
1. `import --help`
2. Import one timer, then globs, then stdin.
3. `validate`
4. `sudo import --write` (staging)
5. `service install --yes`
6. Watch the timers and RunWisp fire together.
7. `systemctl disable --now`
8. `run`, `logs`, `reload`, `promote`.

Fixtures:
- 5 custom timers: nightly with `Persistent`/jitter, `OnUnitActiveSec`+`OnBootSec`, a `Unit=` pointing at a different service, two `OnCalendar` lines, and a per-minute one.
- Drop-in overrides.
- A certbot-style service.
- 15 `OnCalendar` spellings.
- 10 stock Ubuntu timers.
- A user unit.
- An orphan timer.
- A stdin import.

Not tested: the web UI (placeholder embedded), the TUI, a real sandboxed service.

What worked well (don't regress): single-timer import, `Unit=` resolution, cron
conversions for `daily/weekly/monthly/quarterly/yearly`, `Mon..Fri 09..17:00`,
`*:0/15`, `Sat,Sun`, and a trailing timezone. Also: `--write` refuses atomically when
a row would not load, the double-run footer is always printed, and
`run`/`reload`/`promote` behave. The `%T` specifier warning was correct: systemd
really expanded `date +%T` to `date +/tmp` on the box.

## Bugs / real problems (ranked)

### 1. `import systemd --write` replaces everything staged earlier, silently (HIGH)
Repro: stage 4 timers with `--write`, disable their timers in systemd, then
`sudo runwisp import systemd sync.timer --write`.
- `runwisp.d/imported.toml` now holds only `sync`.
- Output says "Staged 1 task". It never mentions the 4 that were dropped.
- `--dry-run` doesn't mention it either.
- `runwisp reload` then removes them (`removed=4`), and their systemd timers are already off. The jobs just stop running.
- This contradicts the docs: "adds imported jobs next to a config you already have", "run it again safely", and the systemd page's "You can move a few units at a time".
- It is the cron semantics (re-import the whole crontab) applied to a per-unit workflow.
- Fix: merge by name for systemd/supervisord. Or at minimum list "- removed from staging: …" and require `--force`.
- Test: a re-import of a different unit keeps the first.

### 2. Drop-in overrides are ignored with no notice (HIGH)
`/etc/systemd/system/backup.timer.d/override.conf` (`OnCalendar=` reset plus a new
time) and `backup.service.d/env.conf` were not read at all: the imported cron was
the base file's 03:30 instead of 01:15, and `EXTRA=1` was missing. No note.
`systemctl edit` is how most people customize a timer, so the imported schedule is
wrong on exactly the boxes where someone cared about it. The docs don't mention
drop-ins either.
- Fix: read `NAME.{timer,service}.d/*.conf` (empty assignment resets the list), or at least a note "N drop-in files next to this unit were not read (`systemctl cat` shows the merged unit)".

### 3. `OnUnitActiveSec=1d`, `1w`, `1h 30min`, `1d 12h` are rejected (MEDIUM-HIGH)
They become `cron = "@every 1d"` under a TODO, and the config doesn't validate
(Go durations have no `d`/`w`). `systemd-tmpfiles-clean.timer` uses `1d`, so
importing it on a stock Ubuntu box fails. systemd's multi-unit spelling `1h 30min`
(with a space) also fails. `90`, `5sec` and `100min` work.
- Fix: parse systemd time spans fully (d, w, spaces, `min`, `h`, `s`) and emit `@every 24h`.

### 4. Footer wording lies about counts and about "TODO" (MEDIUM, confusing)
- With a `%T` specifier or two `OnCalendar` lines, the summary says "2 items carry a # TODO", but the TOML contains zero `# TODO` lines. The issue is a `!` row note only. Someone greps for `TODO` and finds nothing.
- "6 items need a fix before this config loads" and "3 items need a fix…" were printed when only 1 item (the orphan, or the `1d` one) actually made validation fail. The rest loaded fine. `--write` was refused by the real error, so the count overstates it.
- Fix: say "N items need review"; say "can't load" only for rows that actually fail validation.

### 5. The "disable the unit" footer is ambiguous and easy to get wrong (MEDIUM)
It prints `systemctl disable --now <unit>`, which always has a placeholder. A user
who runs it on `backup.service` gets exit 0, a wall of text, and the timer stays
active. Services of timer pairs are `static`, so they can't be disabled.
- Fix: print the real command, e.g. `sudo systemctl disable --now backup.timer cleanup.timer`, built from the imported timers. Print `.timer` for pairs, the `.service` for service imports.

### 6. Nothing warns when imported timers are still enabled once RunWisp is running (MEDIUM)
After `service install`, `heartbeat` and `cleanup` ran twice (systemd and RunWisp
side by side) with no log line, no `status` warning, and no warning from `service
install` (it only mentions cron). The importer knows the unit names, so
`systemctl is-enabled NAME.timer` is cheap. Compare the cron flow: `service
install` does say "cron.service is still running, owns 6 cron jobs".
- Suggest: the same note for enabled systemd timers whose names match the staged tasks.

### 7. Importing the .service of a timer pair gives a run-at-boot task, with no hint (MEDIUM)
`import systemd backup.service` while `backup.timer` sits beside it yields
`run_on_start = "boot"` (backup on every boot, never on schedule). The note only
says "Type=oneshot → run-once-per-boot task". The importer can see the sibling timer.
- Fix: "backup.timer starts this unit. Import the timer instead."

### 8. A glob that matches drop-in directories fails with a bare error (LOW-MEDIUM)
`runwisp import systemd /etc/systemd/system/backup.*` →
`read …/backup.service.d: is a directory`. Skip directories (or say which arg to remove).

### 9. One orphan timer blocks the whole batch (LOW-MEDIUM, human)
With `*.timer` globs, any timer whose service isn't found makes `--write` refuse
everything. The message ("Fix that line in the source…") reads as if the *source*
is broken. A person has to find the orphan in the list, then re-run excluding it.
- Suggest: name the file to exclude (`orphan.timer`), or a `--skip-unresolved`.

### 10. Silently dropped directives despite "nothing is silently dropped" (LOW-MEDIUM)
`ConditionPathExists`, `OnFailure=`, `Nice=`, `TimeoutStartSec=`, `SuccessExitStatus=`
and `StandardOutput=append:` on `certrenew.service` were not mentioned. `OnFailure=` and
`SuccessExitStatus` matter for "failures stay visible". `TimeoutStartSec` may map to
`timeout`. Other units did get "dropped: KillMode…", so this looks inconsistent
rather than designed.

### 11. `man-db.timer` imports the wrong command (LOW)
`ExecStart=+/usr/bin/install …` comes first, `mandb` second. The importer picks the
first (the recovery step) and flags it with `!`, so it's visible. A human has to
open the unit to learn that the useful command is the second one. Maybe show the
dropped commands in the note.

## Small papercuts
- Summary header says "Imported systemd unit → 5 tasks" (singular for many).
- `--quiet` is documented as "suppress the summary" but still prints every `!` row and the double-run footer (by design per `import_summary.go`; the flag help should say so).
- The `%T` note says "Replace them with concrete values". Under RunWisp `date +%T` works as the user meant. The note doesn't say what systemd expanded it to (`/tmp`) or that RunWisp passes it literally, so you can't tell whether to keep or change it. `%%` handling not checked.
- Notes like "RunWisp delays a start only while other jittered tasks are running" for `RandomizedDelaySec` are accurate but long, and repeat per task.
- Silent `catch_up = 0` on every non-`Persistent` timer, with no row note (documented, but invisible when reading the output).
- `import … -o` / `--write` as a non-root user creates a stray `.runwisp/` data dir in the cwd, even when the import was refused.
- `--dry-run` still prints "Disable each one before starting RunWisp" when the daemon is already running.
- The sync case ("add a task per extra schedule") gives no example, and `cron` is a single string.
- Bare unit lookup as a non-root user doesn't search the other user's `~/.config/systemd/user`. The error is clear and says to pass a path.
- No documented way back: cron has `takeover` undo, systemd has none. Back out = re-enable the timers by hand and delete the staged tasks.
- Outside the import: `service install` printed the generated password and says `runwisp password` "won't disclose it", yet the first-boot WARN says "print it with `runwisp password`" (the daemon restarts right after install with the drop-in; the first boot's WARN is stale). `service install` needs `--yes` with no TTY (clear message).
