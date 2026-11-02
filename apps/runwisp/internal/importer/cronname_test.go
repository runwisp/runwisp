// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package importer

import "testing"

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
