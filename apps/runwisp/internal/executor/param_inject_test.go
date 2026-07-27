// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestShellQuote_NeutralisesMetacharacters(t *testing.T) {
	// A value crafted to break out of the command must survive as one inert
	// literal — the core trust-model guarantee for operator-supplied values.
	got := shellQuote(`'; rm -rf / #`)
	assert.Equal(t, `''\''; rm -rf / #'`, got)
}

func TestAppendArgTokens_NoTokensLeavesScriptUnchanged(t *testing.T) {
	assert.Equal(t, "backup.sh", appendArgTokens("backup.sh", nil))
}

func TestAppendArgTokens_QuotesEachToken(t *testing.T) {
	script := appendArgTokens("backup.sh", []string{"/data", "--region", "eu", "--force"})
	assert.Equal(t, `backup.sh '/data' '--region' 'eu' '--force'`, script)
}

func TestAppendArgTokens_SpacesStayOneArgv(t *testing.T) {
	script := appendArgTokens("deploy.sh", []string{"my project"})
	assert.Equal(t, `deploy.sh 'my project'`, script)
}

func TestAppendArgTokens_TrailingNewlineAttachesToFinalCommand(t *testing.T) {
	// A multiline `run` block ends in a newline. Tokens must attach to the
	// final command, not become a separate one — otherwise a value like `id`
	// would run the `id` binary on its own line.
	script := appendArgTokens("echo start\nbackup.sh\n", []string{"id"})
	assert.Equal(t, "echo start\nbackup.sh 'id'", script)
}
