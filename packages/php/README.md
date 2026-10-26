# runwisp/runwisp

Scheduled tasks you can see. `runwisp/runwisp` runs your PHP tasks under
[RunWisp](https://runwisp.com): every run is recorded with its output, exit
code and duration, and shown in a web UI.

```bash
composer require runwisp/runwisp
```

```php
<?php
// runwisp.php
require __DIR__ . '/vendor/autoload.php';

$runwisp = new RunWisp\RunWisp();

$runwisp->task('nightly-backup', ['cron' => '0 3 * * *', 'retry_attempts' => 3], function (RunWisp\Run $run) {
    echo "dumping the database\n";
});

exit($runwisp->main());
```

```
$ php runwisp.php
RunWisp dashboard: http://127.0.0.1:9477, password: vendor/bin/runwisp password --data .runwisp
```

- Each run is a new PHP process, so runs happen at the same time and a stop
  from the web UI really stops one.
- A thrown exception fails the run, with the stack trace in its log.
- Runs once per data directory, however many copies of the worker run.
- Retries, jitter, timeouts and notifications are task options.
- `php runwisp.php check` lists the tasks with their next runs and validates
  the config, exiting with 1 on a mistake, so CI catches it before a deploy.

Framework integrations read the schedule you already have:

- **Laravel**: `php artisan runwisp:work` instead of the `schedule:run` cron entry.
- **Symfony**: `bin/console runwisp:work` runs the Scheduler component's schedules.
- **CodeIgniter 4**: `php spark runwisp:work` runs `codeigniter4/tasks`.

`#[RunWisp\Task(retry_attempts: 3)]` on a command, job or closure adds
RunWisp keys to its task. Each framework has a `runwisp:check` command too.

Linux, macOS and WSL, PHP 8.2 or later. The Composer plugin downloads the
`runwisp` binary of the same version. Read the
[PHP SDK guide](https://docs.runwisp.com/sdk/php/) for every option.

Apache-2.0. The `runwisp` binary it runs is GPL-3.0-or-later.
