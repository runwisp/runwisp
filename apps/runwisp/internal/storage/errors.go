// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package storage

import "errors"

// ErrNotFound is returned when a requested record does not exist.
// Callers should use errors.Is(err, storage.ErrNotFound) to check.
var ErrNotFound = errors.New("record not found")
