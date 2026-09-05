package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runNoAskGuard runs the command erun actually installs and reports its exit
// code: 2 refuses the stop, 0 lets the turn end.
func runNoAskGuard(t *testing.T, shell, input string) int {
	t.Helper()
	cmd := exec.Command(shell, "-c", orchestratorNoAskStopGuardCommand())
	cmd.Stdin = strings.NewReader(input)
	err := cmd.Run()
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	t.Fatalf("run guard: %v", err)
	return 0
}

func writeGuardTranscript(t *testing.T, dir, name, text string) string {
	t.Helper()
	line := map[string]any{"type": "assistant", "message": map[string]any{"content": []any{map[string]any{"text": text}}}}
	encoded, err := json.Marshal(line)
	if err != nil {
		t.Fatalf("encode transcript: %v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, append(encoded, '\n'), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return path
}

// writeGuardTranscriptEntries writes the transcript verbatim, so a test can
// stage what Claude Code actually records around a turn — the hook's own
// firing, the operator's prompt — and not just the turn's own message.
func writeGuardTranscriptEntries(t *testing.T, dir, name string, entries []map[string]any) string {
	t.Helper()
	var encoded []byte
	for _, entry := range entries {
		line, err := json.Marshal(entry)
		if err != nil {
			t.Fatalf("encode transcript entry: %v", err)
		}
		encoded = append(append(encoded, line...), '\n')
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return path
}

func assistantTranscriptEntry(text string) map[string]any {
	return map[string]any{
		"type":    "assistant",
		"message": map[string]any{"content": []any{map[string]any{"text": text}}},
	}
}

func guardHookInput(t *testing.T, transcript string, active bool) string {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{"transcript_path": transcript, "stop_hook_active": active})
	if err != nil {
		t.Fatalf("encode hook input: %v", err)
	}
	return string(encoded)
}

// The guard is a one-liner, so asserting on its text would prove nothing about
// what it does. This runs it.
func TestOrchestratorNoAskStopGuardDecidesOnTheTurnsLastWords(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no POSIX shell on this host")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("no node on this host")
	}
	dir := t.TempDir()
	gate := writeGuardTranscript(t, dir, "gate.jsonl", "Filed it. Say the word and I will open the PR.")
	clean := writeGuardTranscript(t, dir, "clean.jsonl", "Filed it and opened the PR. Both are verified.")
	// A firing is recorded in the transcript, and the record carries this
	// command — every trigger phrase with it. Read as raw lines, the guard
	// matches its own echo from then on and the session can never end.
	echoed := writeGuardTranscriptEntries(t, dir, "echoed.jsonl", []map[string]any{
		assistantTranscriptEntry("Filed it and opened the PR. Both are verified."),
		{"type": "system", "content": "Stop hook feedback: " + orchestratorNoAskStopGuardCommand()},
	})
	// The operator asking a question is not the turn handing one back.
	asked := writeGuardTranscriptEntries(t, dir, "asked.jsonl", []map[string]any{
		{"type": "user", "message": map[string]any{"content": "would you like me to file it?"}},
		assistantTranscriptEntry("Filed it and opened the PR. Both are verified."),
	})

	cases := []struct {
		name  string
		input string
		want  int
	}{
		{"a turn ending on a consent gate is refused", guardHookInput(t, gate, false), 2},
		{"a turn that decided is let go", guardHookInput(t, clean, false), 0},
		{"the guard's own recorded firing is not the turn's last words", guardHookInput(t, echoed, false), 0},
		{"the operator's own question is not the turn's last words", guardHookInput(t, asked, false), 0},
		// Corrected once, never looped.
		{"an already nudged turn is not refused again", guardHookInput(t, gate, true), 0},
		// Fail-open: wedging a session costs more than the stalls this prevents.
		{"an unreadable transcript does not block the stop", guardHookInput(t, filepath.Join(dir, "missing.jsonl"), false), 0},
		{"unparseable hook input does not block the stop", "not json at all", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runNoAskGuard(t, shell, tc.input); got != tc.want {
				t.Fatalf("guard exited %d, want %d", got, tc.want)
			}
		})
	}
}

// countStopHookBlocks tallies the Stop event by owner.
func countStopHookBlocks(t *testing.T, path string) (guards, idles, liveConversations, foreign int) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var settings struct {
		Hooks struct {
			Stop []any `json:"Stop"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	for _, block := range settings.Hooks.Stop {
		switch {
		case isOrchestratorNoAskStopGuardBlock(block):
			guards++
		case isOrchestratorActivityHookBlock(block):
			idles++
		case isOrchestratorLiveConversationHookBlock(block):
			liveConversations++
		default:
			foreign++
		}
	}
	return guards, idles, liveConversations, foreign
}

func seedOperatorStopHook(t *testing.T, dir string) string {
	t.Helper()
	claudeDir := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("seed settings dir: %v", err)
	}
	theirs := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "echo operator-owned"}}}
	seed, err := json.Marshal(map[string]any{"hooks": map[string]any{"Stop": []any{theirs}}})
	if err != nil {
		t.Fatalf("encode seed: %v", err)
	}
	path := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(path, seed, 0o644); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	return path
}

func TestOrchestratorSettingsKeepTheStopGuardBesideTheIdleReport(t *testing.T) {
	dir := t.TempDir()
	settingsPath := seedOperatorStopHook(t, dir)

	// Twice: a rewrite replaces our own blocks rather than stacking copies.
	for range 2 {
		if err := ensureOrchestratorSessionStartHook(dir); err != nil {
			t.Fatalf("ensure hooks: %v", err)
		}
	}

	guards, idles, liveConversations, foreign := countStopHookBlocks(t, settingsPath)
	// The settings file is shared with the operator, so theirs has to survive.
	if guards != 1 || idles != 1 || liveConversations != 1 || foreign != 1 {
		t.Fatalf("Stop must carry one guard, one idle report, one live-conversation recorder and the operator's hook, "+
			"got guards=%d idles=%d liveConversations=%d foreign=%d", guards, idles, liveConversations, foreign)
	}
}
