// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"cmp"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/config"
	"github.com/runwisp/runwisp/apps/runwisp/internal/cronspec"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/runtime"
	"github.com/spf13/cobra"
)

// cronOptions are `runwisp cron`'s flags.
type cronOptions struct {
	count    int
	after    string
	timezone string
	json     bool
}

var cronFlags cronOptions

var cronCmd = &cobra.Command{
	Use:   "cron <expression>",
	Short: "Check a cron expression and show when it fires next",
	Long: `Checks a cron expression against RunWisp's grammar, and prints the next times
it fires, exactly as the daemon would fire a task with it: in the given
timezone, with the same daylight saving handling.

The expression may be one argument or several: runwisp cron 0 3 '*' '*' '*'
works too. An @every interval counts from --after.`,
	Example: strings.TrimSpace(`
  runwisp cron '0 3 * * *'
  runwisp cron '*/15 9-17 * * mon-fri' --timezone Europe/Bratislava --count 10
  runwisp cron '0 3 * * *' --json   # fields, errors and next runs as JSON`),
	Args: cobra.MinimumNArgs(1),
	// Answers from the expression alone: no data dir to create, unlike the
	// root's pre-run.
	PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
	RunE: func(cmd *cobra.Command, args []string) error {
		return runCron(cmd.OutOrStdout(), strings.Join(args, " "), cronFlags, time.Now())
	},
	SilenceErrors: true,
	SilenceUsage:  true,
}

func init() {
	cronCmd.Flags().IntVar(&cronFlags.count, "count", 5, "how many next runs to show")
	cronCmd.Flags().StringVar(&cronFlags.after, "after", "", "show runs after this time, RFC 3339 (default: now)")
	cronCmd.Flags().StringVar(&cronFlags.timezone, "timezone", "", "IANA timezone to evaluate the expression in (default: the host's, the daemon's default)")
	cronCmd.Flags().BoolVar(&cronFlags.json, "json", false, "emit a machine-readable JSON document to stdout instead of the human summary")
}

// cronJSONDoc is the machine-readable form of `runwisp cron`. Fields is
// absent for an invalid expression and for @every, which has none.
type cronJSONDoc struct {
	SchemaVersion int                   `json:"schemaVersion"`
	Valid         bool                  `json:"valid"`
	Expression    string                `json:"expression"`
	Timezone      string                `json:"timezone"`
	Fields        *cronspec.Fields      `json:"fields,omitempty"`
	Next          []time.Time           `json:"next"`
	Errors        []cronspec.FieldError `json:"errors"`
}

// runCron explains expr and lists its next opts.count firings after
// opts.after (default now). An invalid expr returns a userFacingError, after
// the JSON document under --json, so the exit code says so either way.
func runCron(w io.Writer, expr string, opts cronOptions, now time.Time) error {
	loc, after, err := opts.resolve(now)
	if err != nil {
		return err
	}
	doc := cronJSONDoc{SchemaVersion: jsonSchemaVersion, Expression: expr, Timezone: loc.String(), Next: []time.Time{}, Errors: []cronspec.FieldError{}}
	fields, errs := cronspec.Explain(expr)
	if len(errs) == 0 {
		next, err := runtime.NextFirings(&model.Task{Cron: expr}, loc, after, opts.count)
		if err != nil {
			return err
		}
		doc.Valid, doc.Fields, doc.Next = true, fields, next
	} else {
		doc.Errors = errs
	}

	if opts.json {
		if err := writeJSON(w, doc); err != nil {
			return err
		}
	} else if doc.Valid {
		printCron(w, doc)
	}
	if !doc.Valid {
		details := make([]string, len(errs))
		for i, e := range errs {
			details[i] = fmt.Sprintf("%s %q: %s", e.Field, e.Value, e.Message)
		}
		return &userFacingError{title: fmt.Sprintf("%q is not a valid cron expression", expr), details: strings.Join(details, "\n")}
	}
	return nil
}

// resolve checks the flags and returns the zone and the time to count from.
func (opts cronOptions) resolve(now time.Time) (*time.Location, time.Time, error) {
	loc, err := config.ResolveTimezone("--timezone", cmp.Or(opts.timezone, config.ResolveSystemTimezone()))
	if err != nil {
		return nil, now, &userFacingError{title: "invalid --timezone", details: err.Error()}
	}
	if loc == nil {
		loc = time.UTC
	}
	if opts.count < 0 {
		return nil, now, &userFacingError{title: "invalid --count", details: "--count must be 0 or more"}
	}
	if opts.after == "" {
		return loc, now, nil
	}
	after, err := time.Parse(time.RFC3339, opts.after)
	if err != nil {
		return nil, now, &userFacingError{title: "invalid --after", details: fmt.Sprintf("%q is not an RFC 3339 time such as 2026-01-02T15:04:05Z", opts.after)}
	}
	return loc, after, nil
}

func printCron(w io.Writer, doc cronJSONDoc) {
	fmt.Fprintf(w, "✓ %s is valid.\n", doc.Expression)
	if len(doc.Next) == 0 {
		return
	}
	fmt.Fprintf(w, "  next runs (%s):\n", doc.Timezone)
	for _, at := range doc.Next {
		fmt.Fprintf(w, "    %s\n", at.Format("Mon 2006-01-02 15:04:05 MST"))
	}
}
