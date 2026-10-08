// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package autostarttest

import "errors"

// ScriptedPrompter answers prompts from a pre-set queue. It satisfies
// autostart.Prompter without importing it (autostart's own tests import
// this package, so the dependency cannot point the other way).
type ScriptedPrompter struct {
	YesNo    []bool
	Literals []string
	// Mismatch is returned from ConfirmLiteral when the queued answer is not
	// the expected word; tests set it to autostart.ErrAborted.
	Mismatch error
	yesIdx   int
	litIdx   int
}

func (s *ScriptedPrompter) Confirm(_ string, _ bool) (bool, error) {
	if s.yesIdx >= len(s.YesNo) {
		return false, errors.New("ScriptedPrompter: no answer queued for Confirm")
	}
	ans := s.YesNo[s.yesIdx]
	s.yesIdx++
	return ans, nil
}

func (s *ScriptedPrompter) ConfirmLiteral(_, expected string) error {
	if s.litIdx >= len(s.Literals) {
		return errors.New("ScriptedPrompter: no answer queued for ConfirmLiteral")
	}
	ans := s.Literals[s.litIdx]
	s.litIdx++
	if ans != expected {
		return s.Mismatch
	}
	return nil
}
