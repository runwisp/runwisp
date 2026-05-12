// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// readRemotePasswordFrom resolves an operator-supplied password from one of
// the supported input sources. envName is the environment variable consulted
// when source == "env"; source may be "env" / "" (env var) or "-" (single
// line from stdin, no echo).
func readRemotePasswordFrom(envName, source string) (string, error) {
	switch source {
	case "env", "":
		v := os.Getenv(envName)
		if v == "" {
			return "", fmt.Errorf("%s is not set; either export it or pass --password -", envName)
		}
		return v, nil
	case "-":
		r := bufio.NewReader(os.Stdin)
		line, err := r.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", fmt.Errorf("read password from stdin: %w", err)
		}
		v := strings.TrimRight(line, "\r\n")
		if v == "" {
			return "", errors.New("stdin yielded an empty password")
		}
		return v, nil
	default:
		return "", fmt.Errorf("unsupported --password source %q (expected env or -)", source)
	}
}
