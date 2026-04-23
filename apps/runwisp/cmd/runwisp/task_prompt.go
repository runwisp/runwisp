// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/robfig/cron/v3"
	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/model"
	"gopkg.in/yaml.v3"
	"log/slog"
)

var (
	promptTitle = lipgloss.NewStyle().Bold(true)
	promptDim   = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "8", Dark: "245"})
	promptSep   = strings.Repeat("─", 40)
)

// taskDraft holds the mutable state of a task while adding or editing.
type taskDraft struct {
	Name          string
	Description   string
	Cron          string
	Command       string
	Group         string
	API           bool
	Timeout       string
	ConcLimit     int
	ConcPolicy    string
	Restart       string
	Catchup       string
	RetryLimit    int
	RetryDelay    int
	RetentionRuns int
	RetentionAge  string
}

// promptContext carries state for the interactive edit loop.
type promptContext struct {
	confirmLabel  string
	existingNames []string
	originalName  string // non-empty for edit (to allow rename-to-self)
}

func newDraft() *taskDraft {
	return &taskDraft{API: true}
}

func newDraftFromTask(t model.Task) *taskDraft {
	return &taskDraft{
		Name:          t.Name,
		Description:   t.Description,
		Cron:          t.Trigger.Cron,
		Command:       t.Run,
		Group:         t.Group,
		API:           t.Trigger.APIEnabled(),
		Timeout:       t.Execution.Timeout,
		ConcLimit:     t.Execution.Concurrency.Limit,
		ConcPolicy:    string(t.Execution.Concurrency.Policy),
		Restart:       string(t.Execution.Restart),
		Catchup:       string(t.Trigger.Catchup),
		RetryLimit:    t.Retry.Limit,
		RetryDelay:    t.Retry.DelaySec,
		RetentionRuns: t.Retention.Runs,
		RetentionAge:  t.Retention.Age,
	}
}

// toTask converts the draft to a model.Task, including only non-default fields
// so the generated YAML stays minimal.
func (d *taskDraft) toTask() model.Task {
	t := model.Task{
		Description: d.Description,
		Trigger: model.TaskTrigger{
			Cron: d.Cron,
		},
		Run: d.Command,
	}
	if d.Group != "" && d.Group != "Tasks" {
		t.Group = d.Group
	}
	if !d.API {
		f := false
		t.Trigger.API = &f
	}
	if d.Timeout != "" {
		t.Execution.Timeout = d.Timeout
	}
	if d.ConcLimit > 1 {
		t.Execution.Concurrency.Limit = d.ConcLimit
	}
	if d.ConcPolicy != "" && d.ConcPolicy != string(model.PolicyQueue) {
		t.Execution.Concurrency.Policy = model.ConcurrencyPolicy(d.ConcPolicy)
	}
	if d.Restart != "" && d.Restart != string(model.RestartNever) {
		t.Execution.Restart = model.RestartPolicy(d.Restart)
	}
	if d.Catchup != "" && d.Catchup != string(model.MissedRunLatest) {
		t.Trigger.Catchup = model.MissedRunPolicy(d.Catchup)
	}
	if d.RetryLimit > 0 {
		t.Retry.Limit = d.RetryLimit
	}
	if d.RetryDelay > 0 {
		t.Retry.DelaySec = d.RetryDelay
	}
	if d.RetentionRuns > 0 {
		t.Retention.Runs = d.RetentionRuns
	}
	if d.RetentionAge != "" {
		t.Retention.Age = d.RetentionAge
	}
	return t
}

// promptRequiredFields asks for any missing required fields interactively.
func promptRequiredFields(scanner *bufio.Scanner, draft *taskDraft) bool {
	if draft.Name == "" {
		val, ok := readPrompt(scanner, "  Task name: ")
		if !ok || val == "" {
			fmt.Println("  Name is required.")
			return false
		}
		draft.Name = val
	}
	if draft.Command == "" {
		if draft.Cron == "" {
			val, ok := readPrompt(scanner, "  Cron schedule (empty for API-only): ")
			if !ok {
				return false
			}
			if val != "" {
				if err := validateCronExpr(val); err != nil {
					fmt.Printf("  Invalid cron expression: %v\n", err)
					return false
				}
			}
			draft.Cron = val
		}
		val, ok := readPrompt(scanner, "  Command: ")
		if !ok || val == "" {
			fmt.Println("  Command is required.")
			return false
		}
		draft.Command = val
	}
	return true
}

// runTaskEditor drives the interactive prompt loop and persists the result.
// When originalName is empty, the draft is added; otherwise it replaces the
// named task. Returns true when the user confirmed and the write succeeded.
func runTaskEditor(scanner *bufio.Scanner, doc *yaml.Node, draft *taskDraft, ctx promptContext, originalName string) error {
	if !promptTaskLoop(scanner, draft, ctx) {
		fmt.Println("  Cancelled.")
		return nil
	}

	task := draft.toTask()
	if originalName == "" {
		if err := config.AddTaskToDocument(doc, draft.Name, task); err != nil {
			return err
		}
	} else {
		if err := config.UpdateTaskInDocument(doc, originalName, draft.Name, task); err != nil {
			return err
		}
	}
	if err := config.WriteDocument(flags.CfgFile, doc); err != nil {
		return err
	}

	switch {
	case originalName == "":
		slog.Info("Added task", "name", draft.Name, "config", flags.CfgFile)
	case originalName != draft.Name:
		slog.Info("Renamed and updated task", "old", originalName, "new", draft.Name, "config", flags.CfgFile)
	default:
		slog.Info("Updated task", "name", draft.Name, "config", flags.CfgFile)
	}
	return nil
}
func promptTaskLoop(scanner *bufio.Scanner, draft *taskDraft, ctx promptContext) bool {
	for {
		printTaskPreview(draft)
		fmt.Println()
		fmt.Printf("  %s\n", promptSep)
		fmt.Println("  5. More options")
		fmt.Printf("  c. %s   q. Cancel\n", ctx.confirmLabel)
		fmt.Println()

		choice, ok := readPrompt(scanner, "  Enter choice: ")
		if !ok {
			return false
		}
		switch strings.ToLower(choice) {
		case "1":
			oldName := draft.Name
			promptEditField(scanner, "Name", &draft.Name)
			draft.Name = model.SanitizeTaskName(draft.Name)
			if draft.Name != oldName {
				if isNameTaken(draft.Name, ctx.existingNames, ctx.originalName) {
					fmt.Printf("  Task %q already exists. Reverting to %q.\n", draft.Name, oldName)
					draft.Name = oldName
				} else if draft.Name != strings.TrimSpace(draft.Name) {
					fmt.Printf("  Name sanitized to: %s\n", draft.Name)
				}
			}
		case "2":
			promptEditField(scanner, "Description", &draft.Description)
		case "3":
			promptEditCron(scanner, &draft.Cron)
		case "4":
			promptEditField(scanner, "Command", &draft.Command)
		case "5":
			promptAdvanced(scanner, draft)
		case "c":
			if draft.Name == "" {
				fmt.Println("  Name is required.")
				continue
			}
			if strings.TrimSpace(draft.Command) == "" {
				fmt.Println("  Command is required.")
				continue
			}
			return true
		case "q", "":
			return false
		}
	}
}

// promptSelectTask displays a numbered list of tasks and returns the selected index.
func promptSelectTask(scanner *bufio.Scanner, tasks []model.Task) (int, bool) {
	fmt.Println()
	fmt.Printf("  %s\n", promptTitle.Render("Select a task to edit"))
	fmt.Printf("  %s\n", promptSep)
	for i, t := range tasks {
		schedule := t.Trigger.Cron
		if schedule == "" {
			schedule = "API-only"
		}
		desc := t.Description
		if len(desc) > 40 {
			desc = desc[:37] + "..."
		}
		fmt.Printf("  [%d] %-22s %-16s %s\n", i+1, t.Name, schedule, desc)
	}
	fmt.Println()
	val, ok := readPrompt(scanner, "  Enter choice: ")
	if !ok || val == "" {
		return -1, false
	}
	n, err := strconv.Atoi(val)
	if err != nil || n < 1 || n > len(tasks) {
		fmt.Println("  Invalid selection.")
		return -1, false
	}
	return n - 1, true
}

func printTaskPreview(d *taskDraft) {
	fmt.Println()
	fmt.Printf("  %s\n", promptTitle.Render("Task: "+d.Name))
	fmt.Printf("  %s\n", promptSep)
	printField("1", "Name", d.Name)
	printField("2", "Description", valueOrNotSet(d.Description))
	if d.Cron != "" {
		printField("3", "Schedule", d.Cron)
	} else {
		printField("3", "Schedule", promptDim.Render("none (API-only)"))
	}
	printField("4", "Command", formatCommand(d.Command))
	if d.Group != "" && d.Group != "Tasks" {
		printField(" ", "Group", d.Group)
	}
	if !d.API {
		printField(" ", "API trigger", "no")
	}
	if d.Timeout != "" {
		printField(" ", "Timeout", d.Timeout)
	}
	if d.ConcLimit > 1 || (d.ConcPolicy != "" && d.ConcPolicy != string(model.PolicyQueue)) {
		printField(" ", "Concurrency", formatConcurrency(d.ConcLimit, d.ConcPolicy))
	}
	if d.Restart != "" && d.Restart != string(model.RestartNever) {
		printField(" ", "Restart", d.Restart)
	}
	if d.Catchup != "" && d.Catchup != string(model.MissedRunLatest) {
		printField(" ", "Catchup", d.Catchup)
	}
	if d.RetryLimit > 0 {
		printField(" ", "Retry", formatRetry(d.RetryLimit, d.RetryDelay))
	}
	if d.RetentionRuns > 0 || d.RetentionAge != "" {
		printField(" ", "Retention", formatRetention(d.RetentionRuns, d.RetentionAge))
	}
}

func printField(num, label, value string) {
	if num == " " {
		fmt.Printf("     %-14s %s\n", label, value)
	} else {
		fmt.Printf("  %s. %-14s %s\n", num, label, value)
	}
}

func promptEditField(scanner *bufio.Scanner, label string, target *string) {
	prompt := fmt.Sprintf("  %s", label)
	if *target != "" {
		prompt += fmt.Sprintf(" (%s)", *target)
	}
	prompt += ": "
	val, ok := readPrompt(scanner, prompt)
	if !ok {
		return
	}
	if val != "" {
		*target = val
	}
}

func promptEditCron(scanner *bufio.Scanner, target *string) {
	for {
		prompt := "  Schedule"
		if *target != "" {
			prompt += fmt.Sprintf(" [enter=keep, -=clear] (%s)", *target)
		} else {
			prompt += " [empty for API-only]"
		}
		prompt += ": "
		val, ok := readPrompt(scanner, prompt)
		if !ok || val == "" {
			return
		}
		if val == "-" {
			*target = ""
			return
		}
		if err := validateCronExpr(val); err != nil {
			fmt.Printf("  Invalid cron expression: %v\n", err)
			continue
		}
		*target = val
		return
	}
}

func promptAdvanced(scanner *bufio.Scanner, draft *taskDraft) {
	for {
		fmt.Println()
		fmt.Printf("  %s\n", promptTitle.Render("Advanced Options"))
		fmt.Printf("  %s\n", promptSep)
		printField("1", "Group", valueOrDefault(draft.Group, "Tasks"))
		printField("2", "API trigger", boolYesNo(draft.API))
		printField("3", "Timeout", valueOrNotSet(draft.Timeout))
		printField("4", "Concurrency", formatConcurrency(draft.ConcLimit, draft.ConcPolicy))
		printField("5", "Restart", valueOrDefault(draft.Restart, "never"))
		printField("6", "Catchup", valueOrDefault(draft.Catchup, "latest"))
		printField("7", "Retry", formatRetryOrDisabled(draft.RetryLimit, draft.RetryDelay))
		printField("8", "Retention", retentionOrNotSet(draft.RetentionRuns, draft.RetentionAge))
		fmt.Println()
		fmt.Println("  b. Back")
		fmt.Println()
		choice, ok := readPrompt(scanner, "  Enter choice: ")
		if !ok {
			return
		}
		switch strings.ToLower(choice) {
		case "1":
			promptEditField(scanner, "Group", &draft.Group)
		case "2":
			draft.API = !draft.API
			fmt.Printf("  API trigger: %s\n", boolYesNo(draft.API))
		case "3":
			promptEditField(scanner, "Timeout (e.g. 30m, 1h)", &draft.Timeout)
		case "4":
			promptEditConcurrency(scanner, draft)
		case "5":
			promptEditChoice(scanner, "Restart", &draft.Restart, []string{"never", "always", "on-failure"})
		case "6":
			promptEditChoice(scanner, "Catchup", &draft.Catchup, []string{"latest", "all", "none"})
		case "7":
			promptEditRetry(scanner, draft)
		case "8":
			promptEditRetention(scanner, draft)
		case "b", "":
			return
		}
	}
}

func promptEditConcurrency(scanner *bufio.Scanner, draft *taskDraft) {
	limit := draft.ConcLimit
	if limit <= 0 {
		limit = 1
	}
	val, ok := readPrompt(scanner, fmt.Sprintf("  Concurrency limit (%d): ", limit))
	if ok && val != "" {
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			draft.ConcLimit = n
		} else {
			fmt.Println("  Must be a positive integer.")
		}
	}
	promptEditChoice(scanner, "Concurrency policy", &draft.ConcPolicy, []string{"queue", "skip", "terminate"})
}

func promptEditRetry(scanner *bufio.Scanner, draft *taskDraft) {
	val, ok := readPrompt(scanner, fmt.Sprintf("  Retry limit (%d): ", draft.RetryLimit))
	if ok && val != "" {
		if n, err := strconv.Atoi(val); err == nil && n >= 0 {
			draft.RetryLimit = n
		} else {
			fmt.Println("  Must be a non-negative integer.")
		}
	}
	if draft.RetryLimit > 0 {
		val, ok = readPrompt(scanner, fmt.Sprintf("  Retry delay in seconds (%d): ", draft.RetryDelay))
		if ok && val != "" {
			if n, err := strconv.Atoi(val); err == nil && n >= 0 {
				draft.RetryDelay = n
			} else {
				fmt.Println("  Must be a non-negative integer.")
			}
		}
	}
}

func promptEditRetention(scanner *bufio.Scanner, draft *taskDraft) {
	val, ok := readPrompt(scanner, fmt.Sprintf("  Retention runs (%d): ", draft.RetentionRuns))
	if ok && val != "" {
		if n, err := strconv.Atoi(val); err == nil && n >= 0 {
			draft.RetentionRuns = n
		} else {
			fmt.Println("  Must be a non-negative integer.")
		}
	}
	promptEditField(scanner, "Retention age (e.g. 7d, 30d)", &draft.RetentionAge)
}

func promptEditChoice(scanner *bufio.Scanner, label string, target *string, options []string) {
	current := *target
	if current == "" {
		current = options[0]
	}
	val, ok := readPrompt(scanner, fmt.Sprintf("  %s [%s] (%s): ", label, strings.Join(options, "/"), current))
	if !ok || val == "" {
		return
	}
	for _, opt := range options {
		if strings.EqualFold(val, opt) {
			*target = opt
			return
		}
	}
	fmt.Printf("  Invalid option. Choose from: %s\n", strings.Join(options, ", "))
}

func readPrompt(scanner *bufio.Scanner, prompt string) (string, bool) {
	fmt.Print(prompt)
	if !scanner.Scan() {
		return "", false
	}
	return strings.TrimSpace(scanner.Text()), true
}

var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

func validateCronExpr(expr string) error {
	_, err := cronParser.Parse(expr)
	return err
}

func isNameTaken(name string, existing []string, originalName string) bool {
	for _, n := range existing {
		if n == name && n != originalName {
			return true
		}
	}
	return false
}

func valueOrNotSet(s string) string {
	if s == "" {
		return promptDim.Render("(not set)")
	}
	return s
}

func valueOrDefault(s, def string) string {
	if s == "" {
		return promptDim.Render("(" + def + ")")
	}
	return s
}

func boolYesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func formatCommand(cmd string) string {
	lines := strings.Split(strings.TrimRight(cmd, "\n"), "\n")
	if len(lines) <= 1 {
		return cmd
	}
	return lines[0] + promptDim.Render(fmt.Sprintf(" (+%d lines)", len(lines)-1))
}

func formatConcurrency(limit int, policy string) string {
	l := limit
	if l <= 0 {
		l = 1
	}
	p := policy
	if p == "" {
		p = string(model.PolicyQueue)
	}
	return fmt.Sprintf("%d / %s", l, p)
}

func formatRetry(limit, delay int) string {
	if delay > 0 {
		return fmt.Sprintf("%d attempts, %ds delay", limit, delay)
	}
	return fmt.Sprintf("%d attempts", limit)
}

func formatRetryOrDisabled(limit, delay int) string {
	if limit <= 0 {
		return promptDim.Render("(disabled)")
	}
	return formatRetry(limit, delay)
}

func formatRetention(runs int, age string) string {
	parts := make([]string, 0, 2)
	if runs > 0 {
		parts = append(parts, fmt.Sprintf("runs: %d", runs))
	}
	if age != "" {
		parts = append(parts, fmt.Sprintf("age: %s", age))
	}
	return strings.Join(parts, ", ")
}

func retentionOrNotSet(runs int, age string) string {
	s := formatRetention(runs, age)
	if s == "" {
		return promptDim.Render("(not set)")
	}
	return s
}
