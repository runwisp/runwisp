// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/tui/uikit"
)

// --- Palette ---

var (
	purple   = lipgloss.Color("#526ee3")
	green    = lipgloss.Color("#009371")
	yellow   = lipgloss.Color("#FBBF24")
	cyan     = lipgloss.Color("#06B6D4")
	dimGray  = lipgloss.Color("#6B7280")
	darkGray = lipgloss.Color("#374151")
)

var (
	brandMark = lipgloss.NewStyle().Foreground(green).Bold(true)
	brandText = lipgloss.NewStyle().Foreground(purple).Bold(true)
	dimStyle  = lipgloss.NewStyle().Foreground(dimGray)
	dotStyle  = lipgloss.NewStyle().Foreground(darkGray)
	greenMark = lipgloss.NewStyle().Foreground(green)
	yellowSt  = lipgloss.NewStyle().Foreground(yellow)
	cyanBold  = lipgloss.NewStyle().Foreground(cyan).Bold(true)
	boldStyle = lipgloss.NewStyle().Bold(true)
)

// StartTUI launches the interactive Bubble Tea TUI connected to a daemon via API.
// If debugWriter is non-nil, it is wired to the program so that writes to it
// appear in the TUI's debug view.
// launchTicketFunc, when non-nil, enables one-click "Open Web UI" via launch tickets.
// It blocks until the user quits. Returns the chosen uikit.QuitAction and any error.
func StartTUI(info uikit.StartupInfo, client *apiclient.Client, debugWriter *DebugLogWriter, shutdownFunc func() error, launchTicketFunc func() (string, error)) (uikit.QuitAction, error) {
	m := NewModel(TUIConfig{
		Info:             info,
		Client:           client,
		IsRemote:         debugWriter == nil,
		ShutdownFunc:     shutdownFunc,
		LaunchTicketFunc: launchTicketFunc,
	})

	p := tea.NewProgram(m,
		tea.WithAltScreen(),
		tea.WithMouseAllMotion(),
	)

	if debugWriter != nil {
		debugWriter.SetProgram(p)
	}

	// Kill the program on SIGTERM so main() returns normally and the Go
	// coverage runtime can flush GOCOVERDIR data before the process exits.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM)
	defer func() { signal.Stop(sigCh); close(sigCh) }()
	go func() {
		if _, ok := <-sigCh; ok {
			p.Kill()
		}
	}()

	finalModel, err := p.Run()
	if err != nil {
		return uikit.QuitShutdownDaemon, err
	}

	if fm, ok := finalModel.(Model); ok {
		return fm.QuitAction(), nil
	}
	return uikit.QuitShutdownDaemon, nil
}

const (
	fieldPad = 14
	taskPad  = 36
)

// PrintStartup renders the polished startup display to stderr.
func PrintStartup(info uikit.StartupInfo) {
	printStartupTo(os.Stderr, info)
}

// printStartupTo renders the polished startup display to the given writer.
// Exposed as a writer-injecting seam so tests can capture the banner without
// reaching into os.Stderr.
func printStartupTo(w io.Writer, info uikit.StartupInfo) {
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  %s %s %s\n",
		brandMark.Render("⟡"),
		brandText.Render("RunWisp"),
		dimStyle.Render("v"+info.Version),
	)
	fmt.Fprintf(w, "    %s\n", dimStyle.Render(runtime.GOOS+"/"+runtime.GOARCH))
	fmt.Fprintln(w)

	// Database and log paths are deterministic suffixes of Data
	// (<data>/runwisp.db and <data>/logs), so listing them as separate fields
	// padded the banner with three lines for the same root directory. The
	// banner shows the absolute Data dir once; the interactive Info tab still
	// breaks it down for operators who want the full layout.
	printDotField(w, "Config", info.ConfigPath)
	printDotField(w, "Data", info.DataDir)
	if info.Fingerprint != "" {
		printDotField(w, "Fingerprint", info.Fingerprint)
	}
	if info.TLSFingerprint != "" {
		printDotField(w, "TLS cert", "sha256:"+info.TLSFingerprint)
	}
	if info.Timezone != "" {
		tz := info.Timezone
		if info.TimezoneSource != "" {
			tz = fmt.Sprintf("%s (%s)", info.Timezone, info.TimezoneSource)
		}
		printDotField(w, "Timezone", tz)
	}
	fmt.Fprintln(w)

	printCapabilitiesSection(w, info.Capabilities)
	printTasksSection(w, info.Tasks)

	if info.UsingDemo {
		fmt.Fprintf(w, "  %s\n", yellowSt.Render("No config found — running with built-in demo task"))
		fmt.Fprintf(w, "  %s\n", dimStyle.Render("Create runwisp.toml to define your own tasks (see github.com/runwisp/runwisp)"))
		fmt.Fprintln(w)
	}
	if info.CrashedRuns > 0 {
		fmt.Fprintf(w, "  %s\n",
			yellowSt.Render(fmt.Sprintf("Marked %d crashed runs from previous session", info.CrashedRuns)),
		)
	}

	printPendingRunsSection(w, info.PendingRuns)

	if info.CatchUpTriggered > 0 {
		fmt.Fprintf(w, "  %s\n",
			yellowSt.Render(fmt.Sprintf("Triggered %d catch-up runs for missed cron ticks", info.CatchUpTriggered)),
		)
		fmt.Fprintln(w)
	}

	for _, warn := range info.InitWarnings {
		fmt.Fprintf(w, "  %s %s\n", yellowSt.Render("⚠"), warn)
	}
	for _, warn := range info.ScheduleWarnings {
		fmt.Fprintf(w, "  %s %s\n", yellowSt.Render("⚠"), warn)
	}
	if len(info.InitWarnings)+len(info.ScheduleWarnings) > 0 {
		fmt.Fprintln(w)
	}

	// The plaintext password is never rendered in the banner — operators
	// retrieve it via `runwisp password` (CLI) or by selecting Home →
	// Password in the TUI. Anything that captures stderr (journald, log
	// shippers, screen-share recordings) therefore never sees the value.
	if info.PasswordEphemeral {
		fmt.Fprintf(w, "  %s\n", dimStyle.Render("🔑  Ephemeral password generated in memory."))
		fmt.Fprintf(w, "    %s\n", dimStyle.Render("Run `runwisp password` to retrieve it, or open Home in the TUI."))
		fmt.Fprintln(w)
	}

	if info.WebUIDisabled {
		fmt.Fprintf(w, "  %s\n", dimStyle.Render("Web UI disabled (no password in cloud-only mode)"))
	} else if info.ListenURL != "" {
		fmt.Fprintf(w, "  Listening on %s\n", cyanBold.Render(info.ListenURL))
	}
	if info.Headless {
		fmt.Fprintf(w, "  %s\n", dimStyle.Render("Press Ctrl+C to stop."))
	}

	fmt.Fprintln(w)
}

func printCapabilitiesSection(w io.Writer, capabilities []model.CapInfo) {
	var caps []string
	for _, c := range capabilities {
		if c.Available {
			caps = append(caps, greenMark.Render("✓")+" "+c.Name)
		} else {
			caps = append(caps, dimStyle.Render("–")+" "+dimStyle.Render(c.Name))
		}
	}
	fmt.Fprintf(w, "  %s\n", strings.Join(caps, "   "))
	fmt.Fprintln(w)
}

func printTasksSection(w io.Writer, tasks []model.TaskBrief) {
	if len(tasks) == 0 {
		return
	}
	fmt.Fprintf(w, "  %s\n", boldStyle.Render("Tasks"))
	last := len(tasks) - 1
	for i, task := range tasks {
		prefix := "├─"
		if i == last {
			prefix = "└─"
		}
		var schedule string
		switch {
		case task.Kind.IsService():
			schedule = fmt.Sprintf("service x%d", task.Instances)
		case task.Cron != "":
			schedule = task.Cron
		default:
			schedule = "manual"
		}
		dots := strings.Repeat("·", max(2, taskPad-len(task.Name)))
		fmt.Fprintf(w, "  %s %s %s %s\n",
			dimStyle.Render(prefix),
			boldStyle.Render(task.Name),
			dotStyle.Render(dots),
			dimStyle.Render(schedule),
		)
	}
	fmt.Fprintln(w)
}

func printPendingRunsSection(w io.Writer, pr uikit.PendingRunsSummary) {
	if pr.Total == 0 {
		return
	}
	var parts []string
	if pr.Resumed > 0 {
		parts = append(parts, fmt.Sprintf("%d resumed", pr.Resumed))
	}
	if pr.Queued > 0 {
		parts = append(parts, fmt.Sprintf("%d re-queued", pr.Queued))
	}
	if pr.Skipped > 0 {
		parts = append(parts, fmt.Sprintf("%d skipped", pr.Skipped))
	}
	if pr.Failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", pr.Failed))
	}
	fmt.Fprintf(w, "  %s  %s\n",
		dimStyle.Render("Pending runs"),
		strings.Join(parts, dimStyle.Render(" · ")),
	)
	fmt.Fprintln(w)
}

func printDotField(w io.Writer, label, value string) {
	dots := strings.Repeat("·", max(1, fieldPad-len(label)))
	fmt.Fprintf(w, "  %s %s %s\n",
		dimStyle.Render(label),
		dotStyle.Render(dots),
		value,
	)
}

// PrintShutdownComplete renders the goodbye message.
func PrintShutdownComplete() {
	w := os.Stderr
	fmt.Fprintf(w, "  %s Goodbye!\n\n", greenMark.Render("✓"))
}
