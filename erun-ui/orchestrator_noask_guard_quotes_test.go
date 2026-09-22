package main

import (
	"os/exec"
	"strconv"
	"testing"
)

// The guard must refuse a turn that hands the operator a decision without
// refusing a turn that merely reports one. Quoting an earlier violation -- the
// defect text, the contract, the guard's own reason -- is how a verification
// report describes the fix, so a guard that fires on the quotation makes the
// report unwritable. Both directions are asserted here: every quotation is let
// go, and the turn's own unquoted offer is still refused.
func TestOrchestratorNoAskStopGuardReadsOnlyTheTurnsOwnUnquotedWords(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no POSIX shell on this host")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("no node on this host")
	}
	dir := t.TempDir()

	cases := []struct {
		name string
		text string
		want int
	}{
		{
			"a report quoting the violation it describes does not fire",
			`Filed and verified. The guard refused the turn whose last line was "Say the word and I will open the PR." That is the defect it now catches.`,
			0,
		},
		{
			"a blockquoted citation does not fire",
			"Verified against the corrected turn.\n\n> Shall I go ahead and open it?\n\nThe guard refused that turn, which is the point.",
			0,
		},
		{
			"a code-spanned trigger does not fire",
			"Verified. The contract bans closing lines like `let me know if you want the deploy`, and this turn has none.",
			0,
		},
		{
			"a fenced example does not fire",
			"Verified. The regression looked like:\n\n```\nWould you like me to open the PR?\n```\n\nand that turn is now refused.",
			0,
		},
		{
			"a single-quoted citation does not fire",
			"The earlier turn closed with 'next action is yours' and was refused.",
			0,
		},
		{
			"the turn's own unquoted offer still fires",
			"Filed it. Say the word and I will open the PR.",
			2,
		},
		{
			"an offer made beside a quotation still fires",
			`The report calls "shall I proceed?" the defect. For this turn: do you want me to open the PR now?`,
			2,
		},
		{
			"an apostrophe is not a quote delimiter",
			"It's your call, isn't it?",
			2,
		},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeGuardTranscript(t, dir, strconv.Itoa(i)+".jsonl", tc.text)
			if got := runNoAskGuard(t, shell, guardHookInput(t, path, false)); got != tc.want {
				t.Fatalf("guard exited %d, want %d for %q", got, tc.want, tc.text)
			}
		})
	}
}
