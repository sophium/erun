package main

import "strings"

// Two recorders once told the desktop which conversation an orchestrator was
// on: orchestrator-session/<id>.json, and later orchestrator-live/<id>.json
// stamped with a launch nonce. Neither is read any more: an orchestrator runs
// as a Claude background agent, which keeps its own conversation.
//
// Ceasing to install a hook leaves it installed everywhere it already is,
// where it keeps writing on every turn boundary. This removes both, so the
// upgrade actually retires the mechanism instead of only stopping new ones.

// pruneRetiredSessionRecorderHooks drops the recorders from one event's blocks.
// It matches on what a command does -- writes a conversation id into one of
// those directories -- so a block is only removed when it is provably one of
// those hooks and never because it merely sits nearby.
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
