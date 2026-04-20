// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package fingerprint

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math/rand"
	"os"
	"strings"
)

// Generate returns a deterministic human-readable fingerprint based on the
// machine identity and working directory. Running the same binary from the
// same directory on the same machine always produces the same result,
// e.g. "bright-falcon".
func Generate() string {
	machineID := readMachineID()
	cwd, _ := os.Getwd()
	exe, _ := os.Executable()
	hostname, _ := os.Hostname()

	hash := sha256.Sum256([]byte(machineID + "\x00" + cwd + "\x00" + exe + "\x00" + hostname))
	seed := int64(binary.BigEndian.Uint64(hash[0:8]))

	//nolint:gosec // deterministic fingerprint, not crypto
	rng := rand.New(rand.NewSource(seed))
	adj := adjectives[rng.Intn(len(adjectives))]
	noun := nouns[rng.Intn(len(nouns))]
	return fmt.Sprintf("%s-%s", adj, noun)
}

func readMachineID() string {
	for _, path := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
		if data, err := os.ReadFile(path); err == nil {
			if id := strings.TrimSpace(string(data)); id != "" {
				return id
			}
		}
	}
	hostname, _ := os.Hostname()
	return hostname
}

var adjectives = []string{
	"autumn", "hidden", "bitter", "misty", "silent", "empty", "dry", "dark", "summer",
	"icy", "delicate", "quiet", "white", "cool", "spring", "winter", "patient",
	"twilight", "dawn", "crimson", "wispy", "weathered", "blue", "billowing",
	"broken", "cold", "damp", "falling", "frosty", "green", "long", "late", "lingering",
	"bold", "little", "morning", "muddy", "old", "red", "rough", "still", "small",
	"sparkling", "throbbing", "shy", "wandering", "withered", "wild", "black",
	"young", "holy", "solitary", "fragrant", "aged", "snowy", "proud", "floral",
	"restless", "divine", "polished", "ancient", "purple", "lively", "nameless",
}

var nouns = []string{
	"waterfall", "river", "breeze", "moon", "rain", "wind", "sea", "morning",
	"snow", "lake", "sunset", "pine", "shadow", "leaf", "dawn", "glitter", "forest",
	"hill", "cloud", "meadow", "sun", "glade", "bird", "brook", "butterfly",
	"bush", "dew", "dust", "field", "fire", "flower", "firefly", "feather", "grass",
	"haze", "mountain", "night", "pond", "darkness", "snowflake", "silence",
	"sound", "sky", "shape", "surf", "thunder", "violet", "water", "wildflower",
	"wave", "resonance", "wood", "dream", "cherry", "tree", "fog",
	"frost", "voice", "paper", "frog", "smoke", "star",
}
