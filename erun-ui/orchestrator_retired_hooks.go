package main

import "strings"

// The session recorder wrote orchestrator-session/<id>.json so a resume could
// prefer it over the derived conversation id. That override is gone: a
// conversation is derived from the orchestrator id, so nothing reads the file.
//
// Ceasing to install the hook leaves it installed everywhere it already is,
// where it keeps writing on every turn boundary. This removes it, so the
// upgrade actually retires the mechanism instead of only stopping new ones.

// pruneRetiredSessionRecorderHooks drops the recorder from one event's blocks.
// It matches on what the command does -- writes a sessionId into the
// orchestrator-session directory -- so a block is only removed when it is
// provably that hook and never because it merely sits nearby.
func pruneRetiredSessionRecorderHooks(blocks any) []any {
	list, ok := blocks.([]any)
	if !ok {
		return nil
	}
	out := make([]any, 0, len(list))
	for _, block := range list {
		if !isRetiredSessionRecorderBlock(block) {
			out = append(out, block)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func isRetiredSessionRecorderBlock(block any) bool {
	group, ok := block.(map[string]any)
	if !ok {
		return false
	}
	hooks, ok := group["hooks"].([]any)
	if !ok {
		return false
	}
	for _, hook := range hooks {
		entry, ok := hook.(map[string]any)
		if !ok {
			continue
		}
		command, ok := entry["command"].(string)
		if !ok {
			continue
		}
		if strings.Contains(command, "orchestrator-session") && strings.Contains(command, `"sessionId":`) {
			return true
		}
	}
	return false
}
