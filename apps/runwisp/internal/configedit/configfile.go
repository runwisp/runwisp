// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package configedit

import (
	"fmt"
	"os"
)

// WriteNew creates path with the given contents (typically a config starter
// template), refusing to clobber an existing file. The write goes through a Txn
// so a scaffold is never left half-written; there is no gate, because a freshly
// rendered template has nothing to conflict with.
func WriteNew(path, contents string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists", path)
	}
	txn := New()
	txn.Write(path, []byte(contents), DefaultPerm)
	return txn.Apply(nil)
}
