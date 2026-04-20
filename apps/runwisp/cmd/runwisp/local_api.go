// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import "fmt"

func localAPIBaseURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", flags.Port)
}
