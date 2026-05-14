// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/tui/uikit"
	"log/slog"
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

// ConfigureLogger sets up slog for clean runtime output: info level, no timestamps.
func ConfigureLogger() {
	SetLogOutput(os.Stderr)
}

// SetLogOutput redirects slog's default logger to the given writer,
// preserving our level/format settings (info level, no timestamps).
func SetLogOutput(w io.Writer) {
	handler := slog.NewTextHandler(w, &slog.HandlerOptions{
		Level: slog.LevelInfo,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})
	slog.SetDefault(slog.New(handler))
}

// PrintStartup renders the polished startup display to stderr.
func PrintStartup(info uikit.StartupInfo) {
	printStartupTo(os.Stderr, info)
}

// printStartupTo renders the polished startup display to the given writer.
// Exposed as a writer-injecting seam so tests can capture the banner without
// reaching into os.Stderr.
func printStartupTo(w io.Writer, info uikit.StartupInfo) {
	// --- Banner ---
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  %s %s %s\n",
		brandMark.Render("⟡"),
		brandText.Render("RunWisp"),
		dimStyle.Render("v"+info.Version),
	)
	fmt.Fprintf(w, "    %s\n", dimStyle.Render(runtime.GOOS+"/"+runtime.GOARCH))
	fmt.Fprintln(w)

	// --- Configuration ---
	printDotField(w, "Config", info.ConfigPath)
	printDotField(w, "Data", info.DataDir)
	printDotField(w, "Database", info.DBPath)
	printDotField(w, "Logs", info.LogDir)
	if info.Fingerprint != "" {
		printDotField(w, "Fingerprint", info.Fingerprint)
	}
	if info.Timezone != "" {
		tz := info.Timezone
		if info.TimezoneSource != "" {
			tz = fmt.Sprintf("%s (%s)", info.Timezone, info.TimezoneSource)
		}
		printDotField(w, "Timezone", tz)
	}
	fmt.Fprintln(w)

	// --- Capabilities ---
	var caps []string
	for _, c := range info.Capabilities {
		if c.Available {
			caps = append(caps, greenMark.Render("✓")+" "+c.Name)
		} else {
			caps = append(caps, dimStyle.Render("–")+" "+dimStyle.Render(c.Name))
		}
	}
	fmt.Fprintf(w, "  %s\n", strings.Join(caps, "   "))
	fmt.Fprintln(w)

	// --- Tasks ---
	if len(info.Tasks) > 0 {
		fmt.Fprintf(w, "  %s\n", boldStyle.Render("Tasks"))
		last := len(info.Tasks) - 1
		for i, task := range info.Tasks {
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

	// --- Demo notice ---
	if info.UsingDemo {
		fmt.Fprintf(w, "  %s\n", yellowSt.Render("No config found — running with built-in demo task"))
		fmt.Fprintf(w, "  %s\n", dimStyle.Render("Create runwisp.toml to define your own tasks (see github.com/runwisp/runwisp)"))
		fmt.Fprintln(w)
	}

	// --- Crashed runs ---
	if info.CrashedRuns > 0 {
		fmt.Fprintf(w, "  %s\n",
			yellowSt.Render(fmt.Sprintf("Marked %d crashed runs from previous session", info.CrashedRuns)),
		)
	}

	// --- Pending runs ---
	if pr := info.PendingRuns; pr.Total > 0 {
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

	// --- Missed-tick catch-up ---
	if info.CatchUpTriggered > 0 {
		fmt.Fprintf(w, "  %s\n",
			yellowSt.Render(fmt.Sprintf("Triggered %d catch-up runs for missed cron ticks", info.CatchUpTriggered)),
		)
		fmt.Fprintln(w)
	}

	// --- Schedule warnings ---
	for _, warn := range info.ScheduleWarnings {
		fmt.Fprintf(w, "  %s %s\n", yellowSt.Render("⚠"), warn)
	}
	if len(info.ScheduleWarnings) > 0 {
		fmt.Fprintln(w)
	}

	// --- Ephemeral password hint ---
	// The plaintext password is never rendered in the banner — operators
	// retrieve it via `runwisp password` (CLI) or by selecting Home →
	// Password in the TUI. Anything that captures stderr (journald, log
	// shippers, screen-share recordings) therefore never sees the value.
	if info.PasswordEphemeral {
		fmt.Fprintf(w, "  %s\n", dimStyle.Render("🔑  Ephemeral password generated in memory."))
		fmt.Fprintf(w, "    %s\n", dimStyle.Render("Run `runwisp password` to retrieve it, or open Home in the TUI."))
		fmt.Fprintln(w)
	}

	// --- Server ---
	if info.WebUIDisabled {
		fmt.Fprintf(w, "  %s\n", dimStyle.Render("Web UI disabled (no password in cloud-only mode)"))
	} else if info.Port > 0 {
		fmt.Fprintf(w, "  Listening on %s\n",
			cyanBold.Render(fmt.Sprintf("http://localhost:%d", info.Port)),
		)
	}

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

// PrintShutdown renders the shutdown banner.
func PrintShutdown() {
	w := os.Stderr
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  %s %s\n",
		brandMark.Render("⟡"),
		dimStyle.Render("Shutting down..."),
	)
}

// PrintShutdownComplete renders the goodbye message.
func PrintShutdownComplete() {
	w := os.Stderr
	fmt.Fprintf(w, "  %s Goodbye!\n\n", greenMark.Render("✓"))
}
