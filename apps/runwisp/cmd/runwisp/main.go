// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"

	"github.com/charmbracelet/log"
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		log.Error("Fatal error", "err", err)
		os.Exit(1)
	}
}
