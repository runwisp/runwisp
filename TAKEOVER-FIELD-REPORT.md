# `runwisp takeover` field report (fresh VMs, 2026-11-17)

Goal: install RunWisp the way the README says and run `takeover` like a new user on a fresh VPS. Write down every place where it stalls, confuses, or behaves differently from cron.

## Setup

| VM | Image | Cron state before | RunWisp |
| --- | --- | --- | --- |
| A | Ubuntu 24.04.5 cloud image | `cron` active, no anacron | v1.10.0 via `curl -fsSL https://get.runwisp.com \| sh` as non-root `ops` (sudo NOPASSWD) |
| B | Debian 13 genericcloud | **no cron installed** | same; later `apt install cron` |

Seeded on A (a typical small VPS):

- root crontab: `MAILTO=admin@example.com`, `SHELL=/bin/bash`, `PATH=…`, a `tar` backup using `\%F` and redirected to a log, a `find -delete` prune, `@reboot sleep 30 && echo booted >> …`, a `cd /srv/app && flock … ./sync.sh` job, plus one commented-out line
- `ops` crontab: `*/5` healthcheck script, `@daily` report, `15 8 * * 1-5 echo … | logger`
- `/etc/cron.d/myapp`: `MAILTO=""`, a `www-data` php job, a root `env` dump
- stock Ubuntu `/etc/crontab` (`run-parts` lines) and `/etc/cron.d/sysstat`, `/etc/cron.d/e2scrub_all`

Everything here is from v1.10.0, the version new users get today. `main` has unreleased changes, but none of them touch takeover.

---

## TL;DR, ranked

**P0: breaks jobs or trust**

1. [Task names are unstable: adding a crontab line elsewhere renames an existing task and gives its history to another job](#1-task-names-are-unstable-history-moves-to-a-different-job)
2. [Jobs for a user with no home directory fail under RunWisp but run under cron](#2-jobs-for-users-without-a-home-directory-fail-cron-runs-them)
3. [`runwisp service uninstall` without sudo leaves the box with **no scheduler**](#3-service-uninstall-without-sudo-leaves-the-box-with-no-scheduler)
4. [`@reboot` jobs fire at takeover time and are killed 6 ms later by an immediate restart](#4-reboot-job-fires-on-takeover-and-is-killed-by-an-instant-restart)

**P1: first-run friction (each one is a "this tool doesn't work" moment)**

5. [The README's install plus the docs' `sudo runwisp takeover` gives `command not found`](#5-sudo-runwisp-takeover-command-not-found-after-the-official-installer)
6. [Password dead end: the login page, `runwisp password` and `sudo runwisp password` all point somewhere else](#6-password-dead-end)
7. [After takeover, every plain `runwisp …` as the normal user fails or does the wrong thing](#7-after-takeover-the-normal-user-is-locked-out-of-every-plain-command)
8. [`runwisp service status` as the user reports a false DRIFT, plus the wrong binary and data dir](#8-service-status-as-the-user-reports-false-drift)
9. [`crontab -e` silently does nothing until `sudo runwisp reload`, `list` lies about it, and non-root users can't reload](#9-crontab--e-is-silently-ignored-until-a-root-reload)
10. [Two `runwisp` binaries on PATH: upgrades go to the wrong one](#10-two-binaries-on-path-upgrade-skew)
11. [Generated task names are meaningless (`tar`, `echo`, `job`, `sleep`, `find`)](#11-task-names-carry-no-meaning)

**P2: noise, copy, polish**: [items 12 to 24](#p2-noise-copy-polish)

[What went well](#what-went-well) is at the end. Plenty of it is very good.

---

## P0

### 1. Task names are unstable; history moves to a different job

Repro (VM A, after takeover):

```
$ sudo runwisp list | grep echo
echo   15 8 * * 1-5   …          # ops's "weekday standup | logger" job
$ echo '* * * * * www-data cd / && echo hi > /tmp/www2.txt' | sudo tee /etc/cron.d/wwwtest
$ sudo runwisp reload
$ sudo runwisp list | grep echo
echo       * * * * *      …      # now the NEW www-data job
echo-ops   15 8 * * 1-5   …      # the old job, renamed
$ sudo runwisp logs echo-ops
[INFO] No runs yet task=echo-ops
```

The standup job's past runs stay under `echo`, which is now a different job. `runwisp run echo`, any `[[route]]` or notifier keyed on `echo`, bookmarks and API scripts all quietly point at a different job. Collision resolution seems to depend on file order, so adding a file that sorts earlier steals the name.

For a product whose first directive is "failures stay visible", this is the worst finding: the history of a job can switch to another job with no warning.

Fix ideas: once a cron-sourced task has a name, keep it (a persisted owned-name map, see `internal/importer/owned.go` `namer.resolve`, whose `legacy` arg suggests this was meant to exist). A new collision should take the suffixed name itself, not push the suffix onto the older job. At least warn on reload: "task `echo` now refers to a different job".

### 2. Jobs for users without a home directory fail; cron runs them

`/etc/cron.d/myapp`: `*/15 * * * * www-data php -r '…' 2>/dev/null || true`. `www-data`'s home is `/var/www`, which doesn't exist on a fresh Ubuntu or Debian (common whenever nginx or PHP-FPM serves from elsewhere).

RunWisp, every 15 minutes:

```
failed to start shell execution: start command: working_dir "/var/www": stat /var/www: no such file or directory
exit -1
```

Debian's real cron, same user, same missing home, job without a leading `cd`:

```
CRON[3584]: (www-data) CMD (echo nocd-from-cron pwd=$(pwd) > /tmp/nocd.txt)
$ cat /tmp/nocd.txt
nocd-from-cron pwd=/var/spool/cron
```

So a job that worked under cron fails after takeover. (Jobs that start with `cd X &&` aren't affected: the same user with `cd / && …` succeeds under RunWisp.)

Also, the error names `working_dir`, a key the user never wrote; it comes from the importer's `working_dir = "~"`.

Fix: for cron-sourced tasks, match cron and fall back (to `/` or the spool dir) when the home directory is missing, maybe with a warning. Make the error say "home directory of www-data (/var/www) doesn't exist" (`internal/executor/shell.go:177`).

### 3. `service uninstall` without sudo leaves the box with no scheduler

The docs' "Undoing it" section shows `runwisp service uninstall` **without** sudo. Run as `ops`:

```
$ runwisp service uninstall --yes
  1. Run:  sudo systemctl stop runwisp.service
  2. Run:  sudo systemctl disable runwisp.service
  3. Remove unit file …
 ERROR  remove unit: remove /etc/systemd/system/runwisp.service: permission denied
$ systemctl is-active runwisp cron; systemctl is-enabled runwisp cron
inactive  inactive
disabled  masked
```

Steps 1 and 2 ran through sudo, step 3 tried to write as the user and failed. RunWisp is stopped and disabled, cron is still masked, and **nothing runs any job** until someone notices. A later `sudo runwisp service uninstall --yes` recovers it.

Fix: re-exec with sudo up front like `takeover` does (it prints "Taking over cron needs root. Re-running with sudo…"), or check that every step can succeed before taking any. A half-applied uninstall must never leave cron masked. Also fix the docs to show `sudo`.

### 4. `@reboot` job fires on takeover and is killed by an instant restart

Journal right after takeover:

```
18:56:24 Started runwisp.service
18:56:24 Stopping runwisp.service          # same second (password drop-in → restart?)
18:56:24 [INFO] received signal, shutting down … in_flight=1
18:56:24 Started runwisp.service
18:56:24 [INFO] run_on_start = "boot" task already ran this boot; not firing it again task=sleep
```

The `@reboot` task (`sleep 30 && echo booted >> …`) ran on the first start, was stopped after 6 ms, and the second start skipped it. On day one the history shows `sleep  stopped  trigger: startup`.

Two problems:

- Takeover isn't a reboot. Cron already ran the `@reboot` lines when the machine booted, so firing them at takeover is a second run, which can hurt for things like "start my app" lines.
- The instant stop/start shouldn't happen. Write the password drop-in **before** `enable --now`, so the unit starts once.

(An actual reboot afterwards worked: `@reboot` ran once and finished, and cron stayed masked.)

---

## P1

### 5. `sudo runwisp takeover`: command not found after the official installer

The installer, run as a normal user, puts the binary in `~/.local/bin`. The cron docs say to run `sudo runwisp takeover`. sudo's `secure_path` doesn't include `~/.local/bin`:

```
ops@rw-ubuntu:~$ sudo runwisp takeover
sudo: runwisp: command not found
```

`runwisp takeover` (no sudo) works and re-execs itself with sudo. But the docs lead with the sudo form, and the installer's "Next steps" doesn't mention takeover at all, even on a box with 15 cron jobs.

Fix: make the docs and README say `runwisp takeover` (it elevates itself). Have the installer detect crontabs and suggest `runwisp takeover`. Or, when the installer runs as a sudo-capable user, offer `/usr/local/bin`.

### 6. Password dead end

After takeover the user has to log in. Each place they can look:

| Where | What it says | Works? |
| --- | --- | --- |
| Login page, "Where's my password?" | "the daemon randomizes one each boot. Run `runwisp password`" | ✗ wrong: it's fixed and stored in a drop-in |
| `runwisp password` (as user) | socket is root's, run `sudo /home/ops/.local/bin/runwisp password` | ✗ |
| `sudo runwisp password` | "configured with RUNWISP_PASSWORD; RunWisp will not disclose it. Retrieve it from wherever you set it (Docker secret, vault…)" | ✗ the user never set it, RunWisp did |
| Journal on first boot | "this boot generated a random Web UI password; print it with `runwisp password`. It changes on every restart" | ✗ false for a takeover install |
| Takeover's final output | `Password: sudo cat /etc/systemd/system/runwisp.service.d/password.conf` | ✓ only place that's right |

There are contradictions too: takeover says "To rotate it, edit or delete the file", while the file's own header says `# Managed by runwisp service install — DO NOT EDIT`. The cron docs say takeover prints "the commands for the password (`sudo runwisp password`)", which isn't what it prints.

Fix: when the password comes from RunWisp's own `password.conf` drop-in, `sudo runwisp password` should print it, or at least print the path. The login help text should cover the service case. Don't log the "random each boot" warning under a managed unit.

### 7. After takeover, the normal user is locked out of every plain command

As `ops`, after a successful takeover:

| Command | Result |
| --- | --- |
| `runwisp` | "No runwisp.toml at /home/ops. Create a starter with one example task? [Y/n]". Saying yes would start a **second, unrelated daemon**. No mention of the system service. |
| `runwisp status` | error, hint `sudo /home/ops/.local/bin/runwisp status` |
| `runwisp list` | error, hint `sudo /home/ops/.local/bin/runwisp list` |
| `runwisp reload` | "No daemon is running on data dir /home/ops/.runwisp — nothing to reload." (wrong: a daemon is running) |
| `runwisp promote tar` | "can't promote from runwisp.toml — it doesn't load … open runwisp.toml: no such file" (no service hint at all) |
| `runwisp password` | see #6 |

The hints are inconsistent, and the ones that exist point at the stale `~/.local/bin` copy instead of `sudo runwisp …` (`/usr/local/bin` is on sudo's path).

Fix: once a system service exists (the unit file is world-readable), every no-config/no-socket path should say the same thing: "RunWisp runs as the system service. Use `sudo runwisp <cmd>`." Plain `runwisp` in particular must never offer a starter config on a box that already has one. Also consider making the socket group-readable (a `runwisp` group) so operators don't need sudo to read status.

### 8. `service status` as the user reports false DRIFT

Straight after a successful takeover:

```
$ runwisp service status
  Unit file:  /etc/systemd/system/runwisp.service  (DRIFT — unit hash does not match current flags (rerun 'runwisp service install'))
  Binary:     /home/ops/.local/bin/runwisp
  Data dir:   .runwisp  (not writable)
```

With sudo it's correct (`matches recorded settings`, `/usr/local/bin/runwisp`, `/var/lib/runwisp`). Without sudo it compares against the caller's defaults and tells a brand-new user to reinstall. Status should read the binary, config and data dir from the installed unit's `ExecStart`, not from the caller (`cmd/runwisp/cmd_service_status.go:165`).

### 9. `crontab -e` is silently ignored until a root reload

- `ops` added `* * * * * date >> /tmp/ops-minutely.txt`. After 2+ minutes, nothing had run, and nothing told the user why.
- `sudo runwisp list` showed `date * * * * *` straight away, as if it were live (list reads the files, not the daemon).
- `sudo runwisp status` gave no "config changed, reload needed" hint.
- `runwisp reload` as `ops` (a user who can `crontab -e` but has no sudo) → "No daemon is running…". **Non-root users can't apply their own crontab edits at all.**
- For comparison, Debian cron logged `RELOAD (/etc/cron.d/wwwtest)` within a minute, unprompted.

The docs mention reload, and explicit reload is an invariant. But for `include_cron` sources this is the one place where takeover is clearly worse than cron, and it fails silently.

Fix ideas, from smallest: (a) `list`/`status`/TUI/Web UI show "crontab changed since last load, run `sudo runwisp reload`". The daemon already polls cron state every minute for holds, so it can stat the files too. (b) Add a notification/bell entry when a watched crontab changes. (c) Decide whether cron sources count as "operator asked" (they're edited through `crontab`, which is itself an explicit command). That's an invariant question for Riki.

### 10. Two binaries on PATH: upgrade skew

Takeover copies `~/.local/bin/runwisp` to `/usr/local/bin/runwisp`, and the installer put `~/.local/bin` first in `PATH` via `.bashrc`. From then on `runwisp` = user copy, `sudo runwisp` = service copy. `runwisp upgrade` as the user upgrades `update.Executable()`, which is the `~/.local/bin` copy, so `runwisp --version` shows the new version while the service keeps running the old one. Not observed end to end (1.10.0 is the latest release), but the code path is clear (`cmd/runwisp/cmd_upgrade.go:95`).

Fix: after copying, takeover should replace the user copy with a symlink to `/usr/local/bin/runwisp`, or delete it and tell the user. `upgrade` should detect that a system service runs a different binary and upgrade that one (with sudo).

### 11. Task names carry no meaning

Names come from the command's first word:

| Crontab line | Task name |
| --- | --- |
| `tar czf /var/backups/app/…` (comment above: `# nightly backup`) | `tar` |
| `find /var/backups/app -mtime +14 -delete` | `find` |
| `@reboot sleep 30 && echo booted …` | `sleep` |
| `echo "weekday standup" \| logger -t standup` | `echo` |
| `/usr/bin/env > /tmp/cron-env.txt` | `job` |
| `php -r '…'` | `php` |
| `/home/ops/bin/healthcheck.sh` | `healthcheck` ✓ |

The sidebar ends up listing `echo`, `find`, `job`, `sleep`, `sync`, `tar`, and nothing shows **whose** crontab a job came from (root vs ops). The `# nightly backup` comment became the description but not the name. `promote` can't rename either (no `--name`), so `tar` becomes the permanent native name.

Fix ideas: prefer, in order, a preceding comment, then the script basename, then the significant word after wrappers (`sleep … &&`, `cd … &&`, `echo … |`, `/usr/bin/env`, `flock … `), and add the crontab owner for user crontabs (`ops/report`). Show the source (`root crontab`, `ops crontab`, `/etc/cron.d/myapp`) in `list` and the sidebar. Add `promote --as <name>`.

---

## P2: noise, copy, polish

12. **Warning fatigue.** Every `status`, `reload`, `validate`, journal start and the TUI header ("⚠ 5 config warnings") repeat:
    - "N tasks start at the same second … set jitter": on day one, about crontab lines the user can't add `jitter` to without promoting, and cron ran them together anyway. On Debian it fired for 3 jobs that share a minute only once a day.
    - "`MAILTO=""` … Nothing to carry over": a `!` warning that ends with "nothing to do".
    - "another job already took the name debian-sa1": FYI, not a warning.
    - The MAILTO sendmail snippet: useful once, in the takeover plan, not on every reload, and it gets flattened to `\x0a` in the journal.

    Suggest: show these once in the takeover plan, and keep later output to items the user can actually act on. Skip collision warnings for cron-sourced tasks.
13. **The plan shows a count, not the jobs.** "Found 15 cron jobs" with no list or names, then "Take over? [Y/n]". The prompt is the one moment users read carefully, so show the name ← line table (`--dry-run` could print it too).
14. **Web UI Details says "Read-only, as written in runwisp.toml. Edit the file, then run `runwisp reload`"** for a task "Defined in /etc/cron.d/myapp" (`apps/ui/src/lib/components/task/TaskDetailsSheet.svelte:19`). For cron sources it should say "Edit with `crontab -e` / that file, then `sudo runwisp reload`".
15. **Mixed timezones on one page.** Daemon in UTC, browser in CET: the task header says "next run in 14 minutes (19:15)" (daemon TZ) while the run list on the same page says "20:00 · Nov 17" (browser TZ).
16. **Debian with no cron installed:** takeover prints step 1 of a plan ("crontab -e keeps working", on a box with no `crontab` binary), then fails with "masking cron would just remove a scheduler with no work", when there is no cron service at all. No next step. Say "No cron here. To start fresh: `runwisp` (or `sudo runwisp service install`)" and don't print a plan it won't run.
17. **Redirected output looks like no output.** `tar … >> /var/log/backup.log 2>&1` shows "Output: none" in TUI and UI. `promote` knows ("Output goes to /var/log/backup.log; RunWisp can't show it"), but the UI and list never say so. Show that hint on the task page for cron-sourced tasks too.
18. **`promote` copy** lists both hypotheticals ("If a live cron daemon was still running … If cron was already retired …") when it knows which one applies. It also says "run `runwisp reload`" without sudo, though it ran as root. `promote --help` is a 25-line essay.
19. **Promoted block says `on_overlap = "queue"  # cron would overlap instead of queueing`**: a behavior change that takeover itself never mentions. Long jobs that overlapped under cron now queue. Mention it in the takeover plan.
20. **Dashboard on a VPS.** Takeover ends with `Dashboard: http://127.0.0.1:9477`, and the TUI says "Open Web UI", over SSH. Add one line: `ssh -L 9477:127.0.0.1:9477 <host>` when `SSH_CONNECTION` is set.
21. **Uninstall as user without a TTY**: "autostart: requires --yes when stdin is not a terminal" is shown even though step 7 of the plan had just printed. Minor, but read together with #3.
22. **`runwisp logs <task>` for a never-run task** prints `[INFO] No runs yet task=echo-ops`, a log line where a sentence would do.
23. **Start failures report `exit -1`** (and `exit=1` in the CLI summary for the same run). Show "didn't start" instead of a fake exit code.
24. **README Quick Start step 6** says `sudo runwisp service install`, which has the same `command not found` problem as #5 for anyone who used the installer.

---

## What went well

- Install one-liner: fast, checksum-verified, clear PATH message.
- Takeover's self-elevation (`runwisp takeover` → "Re-running with sudo…") is good. It should be the documented form.
- The plan is clear about safety ("Nothing is destructive until RunWisp is in place"), and the failsafe unit is a strong trust signal.
- The guard detection (`e2scrub_all` "does nothing on this box") is smart and was correct.
- Jobs ran as the right user with a cron-like env (`PATH=/usr/bin:/bin`, `HOME`, `LOGNAME`, `SHELL=/bin/sh`). `\%` escaping and the `SHELL`/`PATH` crontab variables were honored.
- Reboot: the service came back, `@reboot` ran once, cron stayed masked.
- Re-running takeover is idempotent (✓ lines, exit 0).
- `sudo runwisp service uninstall` cleanly restored cron (unmasked, enabled, active) and kept config and data.
- `promote` commenting out the crontab line with a pointer note is good, and its "output goes to a file" hint is good.
- The Web UI overview after takeover was immediately useful: the failing `php` job sat in "Needs attention" within seconds. Under cron with `MAILTO=""` that failure would have stayed invisible forever. That's the pitch working.

## Reproducing

Disposable KVM VMs (cloud images + cloud-init seed ISO, user-mode net with SSH port forwards) live in `/tmp/rwvm/`:
`qemu-system-x86_64 -enable-kvm -m 2048 -drive file=/tmp/rwvm/ubuntu.qcow2,if=virtio -cdrom /tmp/rwvm/seed/ubuntu.iso -netdev user,id=n0,hostfwd=tcp:127.0.0.1:2221-:22 -device virtio-net-pci,netdev=n0 -display none`, then `/tmp/rwvm/ssh.sh ubuntu`. The disks are qcow2 overlays of the base images, so `rm` and re-create them for a clean run.
