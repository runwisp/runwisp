// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package importer

import (
	"strings"
	"testing"
)

// TestDeriveCronNameLooksPastNoise covers the commands from the takeover field
// report whose first word said nothing about the job.
func TestDeriveCronNameLooksPastNoise(t *testing.T) {
	for _, tc := range []struct{ command, want string }{
		{`find /var/backups/app -mtime +14 -delete`, "find"},
		{`sleep 30 && echo booted >> /tmp/booted.txt`, "echo"},
		{`sleep 30`, "sleep"},
		{`echo "weekday standup" | logger -t standup`, "logger"},
		{`printf '%s\n' x |& mail -s report ops`, "mail"},
		{`echo hello > /tmp/hello.txt`, "echo"},
		{`/usr/bin/env > /tmp/cron-env.txt`, "env"},
		{`php -r 'file_put_contents("/tmp/x", time());'`, "php"},
		{`/usr/bin/nice -n 19 /opt/sync.sh`, "sync"},
	} {
		if got := deriveCronName(tc.command); got != tc.want {
			t.Errorf("deriveCronName(%q) = %q, want %q", tc.command, got, tc.want)
		}
	}
}

// TestCronNamesCarryTheirSource: a job in a user's crontab or a /etc/cron.d file
// is named after its source as well as its command, so the name says whose job
// it is and no other file can claim it.
func TestCronNamesCarryTheirSource(t *testing.T) {
	ops := CronOptions{User: "ops", NameSuffix: "ops", QualifyNames: true}
	for _, tc := range []struct {
		name, in string
		opts     CronOptions
		want     []string
	}{
		{"short comment names the job", "# nightly backup\n30 2 * * * tar czf /var/backups/app/x.tgz /srv/app\n",
			CronOptions{}, []string{"nightly-backup"}},
		{"long comment falls through", "# rotate the app backups every night\n30 2 * * * tar czf /x.tgz /srv\n",
			CronOptions{}, []string{"tar"}},
		{"user crontab", "*/10 * * * * /home/ops/bin/healthcheck.sh\n", ops, []string{"healthcheck-ops"}},
		{"user crontab with comment", "# nightly backup\n30 2 * * * tar czf /x.tgz /srv\n", ops, []string{"nightly-backup-ops"}},
		{"suffix inside a word is not the suffix", "0 1 * * * /usr/local/bin/stops\n", ops, []string{"stops-ops"}},
		{"repeats in one file", "0 1 * * * /bin/report\n0 2 * * * /bin/report\n", ops, []string{"report-ops", "report-ops-2"}},
		{"suffix already in the name", "30 3 * * 0 root /sbin/e2scrub_all -A -r\n",
			CronOptions{System: true, NameSuffix: "e2scrub_all", QualifyNames: true}, []string{"e2scrub_all"}},
		{"cron.d file", "5-55/10 * * * * root command -v debian-sa1 > /dev/null && debian-sa1 1 1\n",
			CronOptions{System: true, NameSuffix: "sysstat", QualifyNames: true}, []string{"debian-sa1-sysstat"}},
		{"bun-cron title is kept as written", "# bun-cron: weekly-report\n30 2 * * 1 bun run /srv/w.ts\n", ops, []string{"weekly-report"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for _, it := range parseCron(t, tc.in, tc.opts).Items() {
				got = append(got, it.Name)
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("names = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDeriveCronNameNamesPHPFrameworkCommandsAfterTheirSubcommand(t *testing.T) {
	for _, tc := range []struct{ command, want string }{
		{"cd /var/www/parcel && php artisan payments:reconcile >> storage/logs/cron.log 2>&1", "payments-reconcile"},
		{"php artisan schedule:run", "schedule-run"},
		{"/usr/bin/php8.3 /var/www/app/artisan queue:work --stop-when-empty", "queue-work"},
		{"php -d memory_limit=-1 bin/console app:cleanup", "app-cleanup"},
		{"php spark tasks:run", "tasks-run"},
		{"php artisan", "php"},
		{"php /var/www/script.php", "php"},
		{"phpunit artisan x", "phpunit"},
	} {
		if got := deriveCronName(tc.command); got != tc.want {
			t.Errorf("deriveCronName(%q) = %q, want %q", tc.command, got, tc.want)
		}
	}
}

func TestSplitLeadingCd(t *testing.T) {
	for _, tc := range []struct{ in, dir, rest string }{
		{"cd /var/www/parcel && php artisan x", "/var/www/parcel", "php artisan x"},
		{"cd ~/app && ./run.sh", "~/app", "./run.sh"},
		{"cd / && run-parts /etc/cron.daily", "/", "run-parts /etc/cron.daily"},
		{"cd ~ && ./run.sh", "~", "./run.sh"},
		{"cd ~bob/app && ./run.sh", "", "cd ~bob/app && ./run.sh"},
		{"cd ~+ && ./run.sh", "", "cd ~+ && ./run.sh"},
		{"cd app && ./run.sh", "", "cd app && ./run.sh"},
		{`cd "/my dir" && ./run.sh`, "", `cd "/my dir" && ./run.sh`},
		{"cd /srv/$APP && ./run.sh", "", "cd /srv/$APP && ./run.sh"},
		{"cd /srv/app || exit 1", "", "cd /srv/app || exit 1"},
		{"cd /srv/app && ", "", "cd /srv/app && "},
	} {
		dir, rest := splitLeadingCd(tc.in)
		if dir != tc.dir || rest != tc.rest {
			t.Errorf("splitLeadingCd(%q) = %q, %q; want %q, %q", tc.in, dir, rest, tc.dir, tc.rest)
		}
	}
}
