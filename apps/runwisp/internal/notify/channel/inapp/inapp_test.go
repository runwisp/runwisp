// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package inapp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannel_IDAndClose(t *testing.T) {
	c := New("inapp", nil, nil)
	assert.Equal(t, "inapp", c.ID())
	require.NoError(t, c.Close(context.Background()))
}
