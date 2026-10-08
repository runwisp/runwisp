// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package autostart

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
)

// ErrAborted is returned from a Prompter when the user declined.
var ErrAborted = errors.New("autostart: aborted by user")

// ErrNeedsYes is returned when stdin is not a TTY and --yes is missing.
var ErrNeedsYes = errors.New("autostart: requires --yes when stdin is not a terminal")

// Prompter is the user-confirmation seam. Production uses a TTY-aware
// implementation; tests use autostarttest.ScriptedPrompter.
type Prompter interface {
	// Confirm asks a yes/no question. The default fires on bare Enter.
	Confirm(question string, defaultYes bool) (bool, error)
	// ConfirmLiteral asks the user to type expected verbatim. Used
	// for footgun guards like --purge.
	ConfirmLiteral(question, expected string) error
}

// stdioPrompter is the production Prompter.
type stdioPrompter struct {
	in     *bufio.Reader
	out    io.Writer
	isTTY  bool
	autoOK bool // --yes / non-interactive consent
}

// NewStdioPrompter wraps in/out. autoOK=true makes both Confirm and
// ConfirmLiteral succeed without reading from in (for --yes CI runs);
// isTTY=false with autoOK=false returns ErrNeedsYes.
func NewStdioPrompter(in io.Reader, out io.Writer, isTTY, autoOK bool) Prompter {
	return &stdioPrompter{
		in:     bufio.NewReader(in),
		out:    out,
		isTTY:  isTTY,
		autoOK: autoOK,
	}
}

func (p *stdioPrompter) Confirm(question string, defaultYes bool) (bool, error) {
	if p.autoOK {
		return true, nil
	}
	if !p.isTTY {
		return false, ErrNeedsYes
	}
	suffix := "[y/N]"
	if defaultYes {
		suffix = "[Y/n]"
	}
	fmt.Fprintf(p.out, "%s %s ", question, suffix)
	answer, err := p.in.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("read prompt response: %w", err)
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	switch answer {
	case "":
		return defaultYes, nil
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

func (p *stdioPrompter) ConfirmLiteral(question, expected string) error {
	// --yes does NOT skip the literal prompt — that's the whole
	// point of the footgun guard.
	if !p.isTTY {
		return ErrNeedsYes
	}
	fmt.Fprintf(p.out, "%s ", question)
	answer, err := p.in.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read prompt response: %w", err)
	}
	if strings.TrimSpace(answer) != expected {
		return ErrAborted
	}
	return nil
}
