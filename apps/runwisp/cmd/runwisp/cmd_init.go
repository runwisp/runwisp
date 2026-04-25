// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/spf13/cobra"
	"log/slog"
)

var initForce bool

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Scaffold a runwisp.toml with example tasks",
	Long:  `Creates a new runwisp.toml configuration file with commented example tasks and generates a random password. Use --force to overwrite an existing file.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runInit()
	},
}

func init() {
	initCmd.Flags().BoolVarP(&initForce, "force", "f", false, "overwrite existing runwisp.toml")
}

func runInit() error {
	target := flags.CfgFile

	if _, err := os.Stat(target); err == nil && !initForce {
		return fmt.Errorf("%s already exists (use --force to overwrite)", target)
	}

	if initForce {
		if err := config.WriteInitForce(target); err != nil {
			return fmt.Errorf("failed to write %s: %w", target, err)
		}
	} else {
		if err := config.WriteInit(target); err != nil {
			return fmt.Errorf("failed to write %s: %w", target, err)
		}
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
