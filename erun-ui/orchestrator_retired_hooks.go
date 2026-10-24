package main

import "strings"

// The orchestrator-session and orchestrator-live recorder hooks are retired; an
// installed hook keeps writing, so upgrades remove it.

// pruneRetiredSessionRecorderHooks removes only blocks whose command provably is a recorder.
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
		if strings.Contains(command, "conversationId:") && strings.Contains(command, "ERUN_ORCHESTRATOR_LAUNCH") {
			return true
		}
	}
	return false
}
