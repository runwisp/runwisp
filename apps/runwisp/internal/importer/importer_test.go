// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package importer

import (
	"testing"

	"github.com/pelletier/go-toml/v2"
)

// TestTomlStringRoundTripsControlCharacters is the bug: strconv.Quote emits \a,
// \v, \x7f and \x00, none of which TOML accepts, so a crontab command holding
// one made the generated config unparseable.
func TestTomlStringRoundTripsControlCharacters(t *testing.T) {
	values := []string{
		"a\vb", "a\ab", "a\x7fb", "a\x00b", "tab\there", "q\"uote\\slash", "bell\x1b[0m",
		"multi\nline\vwith\x00ctl", "multi\nline\r\nwith \"\"\" quotes", "bad\xffutf8",
	}
	for _, v := range values {
		for name, format := range map[string]func(string) string{"string": tomlString, "verbatim": tomlVerbatimString} {
			var got struct{ V string }
			if err := toml.Unmarshal([]byte("V = "+format(v)+"\n"), &got); err != nil {
				t.Errorf("%s(%q): not valid TOML: %v", name, v, err)
				continue
			}
			want := v
			if want == "bad\xffutf8" {
				want = "bad�utf8"
			}
			if got.V != want {
				t.Errorf("%s(%q): round-tripped to %q", name, v, got.V)
			}
		}
	}
}
