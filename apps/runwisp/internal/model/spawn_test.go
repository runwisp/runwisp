// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package model

import (
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// spawnFields lists every Task field and whether a change to it needs a new
// process. Adding a field to Task fails TestSpawnConfigClassifiesEveryTaskField
// until it is classified here, which forces the "does the executor read this
// at spawn time?" question instead of defaulting silently.
var spawnFields = map[string]bool{
	// Spawn-time: read from the definition the process started under.
	"Run": true, "ExecutionDef": true, "Shell": true, "WorkingDir": true, "Umask": true,
	"RunUser": true, "EnvBase": true, "Env": true, "Secrets": true, "Parameters": true,
	"Timeout": true, "GracefulStop": true, "StopSignal": true, "LogMaxSize": true,
	"LogOnFull": true, "Failures": true, "HealthCheck": true, "Instances": true,

	// Live: read from the current definition (or only by the scheduler, boot,
	// retention, notifications and the API), or only metadata.
	"Name": false, "Kind": false, "Group": false, "Description": false, "Cron": false,
	"Timezone": false, "Jitter": false, "ManualTrigger": false, "CatchUp": false,
	"RunOnStart": false, "RunOnStartMode": false, "WarnAfter": false, "Restart": false,
	"MaxConcurrent": false, "MaxQueued": false, "OnOverlap": false, "RestartDelay": false,
	"RestartBackoff": false, "HealthyAfter": false, "RestartAttempts": false, "Priority": false,
	"Autostart": false, "DependsOn": false, "RetryAttempts": false, "RetryDelay": false,
	"RetryBackoff": false, "KeepRuns": false, "KeepFor": false, "EnvFile": false,
	"SecretsFile": false, "HookTokens": false, "Compose": false, "Source": false,
	"SourceFile": false, "HeldBy": false, "FailureSpec": false, "Ephemeral": false,
	"StationDeclared": false,
}

func TestSpawnConfigClassifiesEveryTaskField(t *testing.T) {
	ty := reflect.TypeFor[Task]()
	for f := range ty.Fields() {
		_, ok := spawnFields[f.Name]
		assert.True(t, ok, "Task.%s is not classified in spawnFields", f.Name)
	}
	assert.Len(t, spawnFields, ty.NumField(), "spawnFields names a field Task no longer has")

	// The explicit comparison must agree with the table.
	cfg := reflect.TypeFor[spawnConfig]()
	for f := range cfg.Fields() {
		assert.True(t, spawnFields[f.Name], "spawnConfig.%s is classified as live", f.Name)
	}
	spawn := 0
	for _, isSpawn := range spawnFields {
		if isSpawn {
			spawn++
		}
	}
	assert.Equal(t, cfg.NumField(), spawn, "spawnFields marks a field spawn-time that spawnConfig does not compare")
}

func TestSpawnDiffers(t *testing.T) {
	base := func() *Task { return &Task{Name: "svc", Kind: KindService, Run: "x", Instances: 1} }
	d := time.Minute

	for name, mutate := range map[string]func(*Task){
		"Run":          func(t *Task) { t.Run = "y" },
		"Shell":        func(t *Task) { t.Shell = "/bin/bash" },
		"WorkingDir":   func(t *Task) { t.WorkingDir = "/srv" },
		"Umask":        func(t *Task) { t.Umask = "0077" },
		"RunUser":      func(t *Task) { t.RunUser = "nobody" },
		"EnvBase":      func(t *Task) { t.EnvBase = "clean" },
		"Env":          func(t *Task) { t.Env = map[string]string{"A": "1"} },
		"Secrets":      func(t *Task) { t.Secrets = map[string]string{"A": "1"} },
		"Timeout":      func(t *Task) { t.Timeout = &d },
		"GracefulStop": func(t *Task) { t.GracefulStop = &d },
		"StopSignal":   func(t *Task) { t.StopSignal = "SIGINT" },
		"LogMaxSize":   func(t *Task) { t.LogMaxSize = 1 },
		"LogOnFull":    func(t *Task) { t.LogOnFull = "kill" },
		"HealthCheck":  func(t *Task) { t.HealthCheck = &Task{Run: "probe"} },
		"Instances":    func(t *Task) { t.Instances = 2 },
	} {
		changed := base()
		mutate(changed)
		assert.True(t, base().SpawnDiffers(changed), "%s must restart a running service", name)
	}

	for name, mutate := range map[string]func(*Task){
		"Description":  func(t *Task) { t.Description = "d" },
		"Restart":      func(t *Task) { t.Restart = RestartNever },
		"RestartDelay": func(t *Task) { t.RestartDelay = &d },
		"KeepRuns":     func(t *Task) { n := 3; t.KeepRuns = &n },
		"EnvFile":      func(t *Task) { t.EnvFile = ".env" },
		"Source":       func(t *Task) { t.Source = SourceStaged },
	} {
		changed := base()
		mutate(changed)
		assert.False(t, base().SpawnDiffers(changed), "%s is read live and must not restart", name)
	}
}
