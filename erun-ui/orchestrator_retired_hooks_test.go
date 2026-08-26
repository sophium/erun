package main

import "testing"

func recorderBlock() map[string]any {
	return map[string]any{"hooks": []any{map[string]any{
		"type":    "command",
		"command": `printf '{"sessionId":"%s"}' "$sid" > /x/orchestrator-session/$ERUN_ORCHESTRATOR_ID.json`,
	}}}
}

func namedBlock(command string) map[string]any {
	return map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command}}}
}

// Ceasing to install the recorder leaves it installed on every machine that
// already ran an older build, still writing a file nothing reads.
func TestRetiredRecorderIsRemovedWhereverItIsInstalled(t *testing.T) {
	out := pruneRetiredSessionRecorderHooks([]any{
		namedBlock("echo busy > /x/orchestrator-activity/$ERUN_ORCHESTRATOR_ID.json"),
		recorderBlock(),
		namedBlock("the operator's own hook"),
	})
	if len(out) != 2 {
		t.Fatalf("expected the recorder removed and the rest kept, got %d blocks", len(out))
	}
	for _, block := range out {
		if isRetiredSessionRecorderBlock(block) {
			t.Fatal("a recorder block survived")
		}
	}
}

// It matches on what the command does, so a block is never removed for merely
// mentioning one of the two markers -- the operator's hooks live here too.
func TestPruneKeepsBlocksThatOnlyResembleTheRecorder(t *testing.T) {
	keep := []any{
		namedBlock("grep orchestrator-session /tmp/notes"),
		namedBlock(`echo '"sessionId": read only'`),
		namedBlock("unrelated"),
	}
	if got := pruneRetiredSessionRecorderHooks(keep); len(got) != 3 {
		t.Fatalf("expected all three kept, got %d", len(got))
	}
	if pruneRetiredSessionRecorderHooks("not a list") != nil {
		t.Fatal("a shape it cannot read must not be rewritten into one it can")
	}
}
