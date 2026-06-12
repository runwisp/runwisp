// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package importer

import (
	"strings"
	"testing"
)

func parseSup(t *testing.T, in string) *Result {
	t.Helper()
	res, err := ParseSupervisordReader(strings.NewReader(in))
	if err != nil {
		t.Fatalf("ParseSupervisordReader: %v", err)
	}
	return res
}

func TestSupervisordBasicProgram(t *testing.T) {
	in := `[program:web]
command=/usr/bin/gunicorn app:app
directory=/srv/app
user=www-data
`
	res := parseSup(t, in)
	out := res.TOML()
	mustContain(t, out, "[services.web]")
	mustContain(t, out, `run = "/usr/bin/gunicorn app:app"`)
	mustContain(t, out, `working_dir = "/srv/app"`)
	mustContain(t, out, `user = "www-data"`)

	tasks, services := res.Counts()
	if tasks != 0 || services != 1 {
		t.Fatalf("counts: got %d/%d want 0/1", tasks, services)
	}
}

func TestSupervisordAutorestartService(t *testing.T) {
	// true and unexpected (and an omitted value) are always-on services; a
	// service is always-restart, so no restart key is emitted.
	for _, value := range []string{"true", "unexpected"} {
		res := parseSup(t, "[program:x]\ncommand=/bin/x\nautorestart="+value+"\n")
		out := res.TOML()
		mustContain(t, out, "[services.x]")
		mustNotContain(t, out, "restart =")
		tasks, services := res.Counts()
		if tasks != 0 || services != 1 {
			t.Fatalf("%s: counts %d/%d want 0/1", value, tasks, services)
		}
	}
}

func TestSupervisordAutorestartFalseIsTask(t *testing.T) {
	res := parseSup(t, "[program:x]\ncommand=/bin/x\nautorestart=false\n")
	out := res.TOML()
	mustContain(t, out, "[tasks.x]")
	mustContain(t, out, "run_on_start = true")
	mustContain(t, out, `restart = "never"`)
	tasks, services := res.Counts()
	if tasks != 1 || services != 0 {
		t.Fatalf("counts %d/%d want 1/0", tasks, services)
	}
}

func TestSupervisordNumprocs(t *testing.T) {
	res := parseSup(t, "[program:worker]\ncommand=/bin/worker\nnumprocs=4\n")
	out := res.TOML()
	mustContain(t, out, "instances = 4")
	found := false
	for _, n := range res.Notes {
		if strings.Contains(n.Message, "RUNWISP_INSTANCE_INDEX") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected instance-index note, got %+v", res.Notes)
	}
}

func TestSupervisordDurationsAndRetries(t *testing.T) {
	in := `[program:api]
command=/bin/api
startsecs=10
startretries=5
stopwaitsecs=30
stopsignal=INT
`
	out := parseSup(t, in).TOML()
	mustContain(t, out, `healthy_after = "10s"`)
	mustContain(t, out, "start_retries = 5")
	mustContain(t, out, `graceful_stop = "30s"`)
	mustContain(t, out, `stop_signal = "SIGINT"`)
}

func TestSupervisordExitCodes(t *testing.T) {
	out := parseSup(t, "[program:x]\ncommand=/bin/x\nexitcodes=0,2\n").TOML()
	mustContain(t, out, "exit_codes = [0, 2]")
}

func TestSupervisordEnvironment(t *testing.T) {
	in := `[program:x]
command=/bin/x
environment=KEY1="value one",KEY2=plain,PARTS="a,b,c"
`
	out := parseSup(t, in).TOML()
	mustContain(t, out, "[services.x.env]")
	mustContain(t, out, `KEY1 = "value one"`)
	mustContain(t, out, `KEY2 = "plain"`)
	mustContain(t, out, `PARTS = "a,b,c"`)
}

func TestSupervisordProgramName(t *testing.T) {
	out := parseSup(t, "[program:web]\ncommand=/bin/run --name %(program_name)s\n").TOML()
	mustContain(t, out, `run = "/bin/run --name web"`)
}

func TestSupervisordUnresolvedExpansion(t *testing.T) {
	res := parseSup(t, "[program:web]\ncommand=/bin/run --idx %(process_num)s\n")
	if !hasAttentionNote(res, "%(...)s") {
		t.Fatalf("expected unresolved-expansion note, got %+v", res.Notes)
	}
	if !res.Items()[0].Attention {
		t.Fatalf("expected attention flag on item")
	}
}

func TestSupervisordGroup(t *testing.T) {
	in := `[group:site]
programs=web,worker

[program:web]
command=/bin/web

[program:worker]
command=/bin/worker
`
	out := parseSup(t, in).TOML()
	mustContain(t, out, `group = "site"`)
}

func TestSupervisordLogFilesDropped(t *testing.T) {
	in := `[program:x]
command=/bin/x
stdout_logfile=/var/log/x.log
stderr_logfile=/var/log/x.err
`
	res := parseSup(t, in)
	mustNotContain(t, res.TOML(), "/var/log/x.log")
	found := false
	for _, n := range res.Notes {
		if strings.Contains(n.Message, "captures stdout and stderr") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected log-dropped note, got %+v", res.Notes)
	}
}

func TestSupervisordSkipsDaemonSections(t *testing.T) {
	in := `[unix_http_server]
file=/tmp/supervisor.sock

[supervisord]
logfile=/tmp/supervisord.log

[program:x]
command=/bin/x
`
	res := parseSup(t, in)
	tasks, services := res.Counts()
	if tasks != 0 || services != 1 {
		t.Fatalf("counts: got %d/%d want 0/1", tasks, services)
	}
}

func TestSupervisordEventListenerNoted(t *testing.T) {
	res := parseSup(t, "[eventlistener:memmon]\ncommand=memmon -a 200MB\n")
	if !hasAttentionNote(res, "isn't supported") {
		t.Fatalf("expected eventlistener attention note, got %+v", res.Notes)
	}
}

func TestSupervisordIncludeFromStdinNoted(t *testing.T) {
	res := parseSup(t, "[include]\nfiles=conf.d/*.conf\n")
	if !hasAttentionNote(res, "stdin") {
		t.Fatalf("expected stdin-include note, got %+v", res.Notes)
	}
}

func TestSupervisordContinuationLines(t *testing.T) {
	in := "[program:x]\ncommand=/bin/x\nenvironment=A=\"1\",\n    B=\"2\"\n"
	out := parseSup(t, in).TOML()
	mustContain(t, out, `A = "1"`)
	mustContain(t, out, `B = "2"`)
}
