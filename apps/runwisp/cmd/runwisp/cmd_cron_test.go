// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunCron(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

	var out bytes.Buffer
	require.NoError(t, runCron(&out, "0 3 * * *", cronOptions{count: 2, timezone: "Europe/Bratislava"}, now))
	assert.Equal(t, "✓ 0 3 * * * is valid.\n  next runs (Europe/Bratislava):\n    Sun 2026-10-11 03:00:00 CEST\n    Mon 2026-10-12 03:00:00 CEST\n", out.String())

	out.Reset()
	require.NoError(t, runCron(&out, "*/30 * * * *", cronOptions{count: 1, timezone: "UTC", after: "2026-01-02T03:04:05Z", json: true}, now))
	var doc cronJSONDoc
	require.NoError(t, json.Unmarshal(out.Bytes(), &doc))
	assert.True(t, doc.Valid)
	assert.Equal(t, "UTC", doc.Timezone)
	assert.Equal(t, []int{0, 30}, doc.Fields.Minute)
	require.Len(t, doc.Next, 1)
	assert.True(t, time.Date(2026, 1, 2, 3, 30, 0, 0, time.UTC).Equal(doc.Next[0]))
	assert.Empty(t, doc.Errors)

	// Invalid: the JSON document still goes to stdout, and the error sets the exit code.
	out.Reset()
	err := runCron(&out, "0 25 * * *", cronOptions{count: 1, json: true}, now)
	_, userFacing := isUserFacing(err)
	assert.True(t, userFacing)
	doc = cronJSONDoc{}
	require.NoError(t, json.Unmarshal(out.Bytes(), &doc))
	assert.False(t, doc.Valid)
	assert.Nil(t, doc.Fields)
	assert.Empty(t, doc.Next)
	require.Len(t, doc.Errors, 1)
	assert.Equal(t, "hour", doc.Errors[0].Field)

	for _, opts := range []cronOptions{{timezone: "Nowhere/Land"}, {after: "tomorrow"}, {count: -1}} {
		_, userFacing = isUserFacing(runCron(&out, "@daily", opts, now))
		assert.True(t, userFacing, "%+v", opts)
	}
}
