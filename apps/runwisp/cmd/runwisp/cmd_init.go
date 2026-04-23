// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"log/slog"
)

var initForce bool

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Scaffold a runwisp.yaml with example tasks",
	Long:  `Creates a new runwisp.yaml configuration file with commented example tasks and generates a random password. Use --force to overwrite an existing file.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runInit()
	},
}

func init() {
	initCmd.Flags().BoolVarP(&initForce, "force", "f", false, "overwrite existing runwisp.yaml")
}

const configTemplate = `# RunWisp configuration
# Documentation: https://github.com/runwisp/runwisp
storage:
  maxSize: 5gb
  minFreeSpace: 500mb

defaults:
  timeout: 1h
  logs:
    maxSize: 100mb
    overflow: tail
  retention:
    runs: 50
    age: 30d

tasks:
  hello-world:
    description: "A simple hello-world task"
    trigger:
      cron: "*/5 * * * *"
    execution:
      concurrency:
        limit: 1
        policy: queue
    run: |
      echo "Hello from RunWisp!"
      echo "Current time: $(date)"

  backup:
    description: "Database backup (example)"
    trigger:
      cron: "0 2 * * *"
    execution:
      timeout: 30m
      concurrency:
        limit: 1
        policy: skip
    retention:
      runs: 30
    run: |
      pg_dump mydb > /backups/mydb-$(date +%%F).sql
`

func runInit() error {
	target := flags.CfgFile

	if _, err := os.Stat(target); err == nil && !initForce {
		return fmt.Errorf("%s already exists (use --force to overwrite)", target)
	}

	if err := os.WriteFile(target, []byte(configTemplate), 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", target, err)
	}

	password := generateInitPassword()

	slog.Info("Created configuration file", "path", target)
	fmt.Println()
	fmt.Println("  Generated password: " + password)
	fmt.Println()
	fmt.Println("  Start RunWisp:")
	fmt.Printf("    RUNWISP_PASSWORD=%s runwisp\n", password)
	fmt.Println()
	fmt.Println("  Or set it in your environment:")
	fmt.Printf("    export RUNWISP_PASSWORD=%s\n", password)
	fmt.Println("    runwisp")
	fmt.Println()
	fmt.Println("  Or just run without a password (one will be generated):")
	fmt.Println("    runwisp")
	fmt.Println()

	return nil
}

func generateInitPassword() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "change-me-" + fmt.Sprintf("%d", os.Getpid())
	}
	return hex.EncodeToString(b)
}
