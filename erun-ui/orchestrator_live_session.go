package main

import "strings"

// An orchestrator's conversation is DERIVED from its id, never looked up. The
// derivation is a pure function, so the mapping is the same on every launch and
// on every machine, and there is nothing on disk for a second session to
// overwrite.
//
// It used to be looked up: a shell hook wrote the live session_id into a file
// per orchestrator, keyed on $ERUN_ORCHESTRATOR_ID, and a resume preferred that
// record over the derived id. The record was written by whichever session held
// that env var, with no ordering and no ownership, so a single launch under the
// wrong id pointed an orchestrator at another's conversation -- and then kept
// confirming it, because the session really was running with that variable.
// Three orchestrators drifted onto conversations that were not theirs and
// stayed there for days, each abandoning its own history.
//
// The lookup existed to follow a compaction fork, on the premise that Claude
// Code moves a resumed conversation to a new session id when it compacts. It
// does not: a resumed session keeps its id across compaction, and forking is
// opt-in (`--fork-session`), which erun never passes. So the mechanism solved a
// problem that did not exist and created one that did.

// orchestratorResumeConversationID answers which conversation a launch should
// resume. A named orchestrator always resumes its own derived conversation. A
// transient one (Investigate) has no id to derive from and keeps whatever it was
// recorded with.
func orchestratorResumeConversationID(orchestratorID, recordedConversationID string) string {
	if id := strings.TrimSpace(orchestratorID); id != "" {
		return orchestratorSessionID(id)
	}
	return strings.TrimSpace(recordedConversationID)
}
