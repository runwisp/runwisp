// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check if the daemon is alive",
	Long:  `Pings the daemon health endpoint to verify it is running and responsive.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runStatus()
	},
}

func runStatus() error {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(localAPIBaseURL() + "/health")
	if err != nil {
		return fmt.Errorf("daemon is not reachable at :%d — %w", flags.Port, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("daemon returned status %d", resp.StatusCode)
	}

	statsResp, err := client.Get(localAPIBaseURL() + "/api/system")
	if err != nil {
		fmt.Printf("RunWisp is healthy at :%d\n", flags.Port)
		return nil
	}
	defer statsResp.Body.Close()

	if statsResp.StatusCode == http.StatusOK {
		var stats map[string]interface{}
		if err := json.NewDecoder(statsResp.Body).Decode(&stats); err == nil {
			fmt.Printf("RunWisp is healthy at :%d\n", flags.Port)
			printSystemStats(stats)
			return nil
		}
	}

	fmt.Printf("RunWisp is healthy at :%d\n", flags.Port)
	return nil
}

func printSystemStats(stats map[string]interface{}) {
	if v, ok := stats["version"]; ok {
		fmt.Printf("  Version:  %v\n", v)
	}
	if v, ok := stats["uptime"]; ok {
		fmt.Printf("  Uptime:   %v\n", v)
	}
	if v, ok := stats["cpuCores"]; ok {
		fmt.Printf("  CPU:      %.0f cores\n", v)
	}
	if v, ok := stats["host"]; ok {
		fmt.Printf("  Host:     %v\n", v)
	}
}
