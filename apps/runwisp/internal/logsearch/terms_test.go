// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package logsearch

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// grepTermsCases is the log:grep matcher conformance list, shared with the
// station, whose own matcher runs the same cases.
const grepTermsCases = "../../../../packages/asyncapi/fixtures/log-grep-terms.json"

func TestTerms_Conformance(t *testing.T) {
	raw, err := os.ReadFile(grepTermsCases)
	require.NoError(t, err)
	var cases []struct {
		Name  string `json:"name"`
		Terms []struct {
			Parts     []string `json:"parts"`
			WordStart bool     `json:"wordStart"`
			WordEnd   bool     `json:"wordEnd"`
		} `json:"terms"`
		CaseSensitive bool   `json:"caseSensitive"`
		Line          string `json:"line"`
		Match         bool   `json:"match"`
	}
	require.NoError(t, json.Unmarshal(raw, &cases))
	require.GreaterOrEqual(t, len(cases), 30)
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			terms := make([]Term, len(c.Terms))
			for i, term := range c.Terms {
				terms[i] = Term{Parts: term.Parts, WordStart: term.WordStart, WordEnd: term.WordEnd}
			}
			m, err := NewTerms(terms, c.CaseSensitive)
			require.NoError(t, err)
			assert.Equal(t, c.Match, m.Match(c.Line), "line %q", c.Line)
		})
	}
}

func TestNewTerms_RejectsEmpty(t *testing.T) {
	for name, terms := range map[string][]Term{
		"no terms":   nil,
		"no parts":   {{}},
		"empty part": {{Parts: []string{"a", ""}}},
	} {
		_, err := NewTerms(terms, false)
		assert.Error(t, err, name)
	}
}
