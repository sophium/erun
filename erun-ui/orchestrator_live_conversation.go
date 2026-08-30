package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// "Which conversation is this orchestrator's by convention" and "which
// conversation is this orchestrator on right now" are two questions, and only
// the first one can be derived. uuid5(namespace, orchestratorID) answers the
// first perfectly: it is the same on every launch and on every machine, so it
// cannot go stale and nothing can point it elsewhere. It does not answer the
// second -- the harness does not always adopt the id erun asks it to resume,
// and an operator's own `/clear` or a compaction starts a new conversation
// under a new id -- but a launch resumes the derived answer anyway, every
// time, unless the operator has explicitly attached something else.
//
// That is a deliberate trade, not an oversight (erun#1696). The alternative --
// silently adopting whatever conversation a previous session happened to drift
// onto -- has its own failure: nothing ever moves a drifted record back, so one
// `/clear` used to rebind an orchestrator to that conversation permanently,
// with every later launch reporting the same "surprising" rebinding forever
// rather than the one-time event it actually was. Resuming the anchor bounds
// that drift to the lifetime of the session that caused it: the next launch
// starts clean, and the conversation it left behind is never lost -- it stays
// recorded (see readOrchestratorLiveConversation) and offered in the Manage
// dialog's Conversation section for the operator to attach deliberately (see
// orchestratorConversationRoles in orchestrator_conversations.go). What it no
// longer does is override the anchor without being asked.
//
// A record like this was removed once, and for a good reason: its writer keyed
// purely on $ERUN_ORCHESTRATOR_ID, so any session that ever ran under the wrong
// id left a record claiming another orchestrator's conversation, and a resume
// then adopted it. Worse, the writer was deleted while the reader stayed, and a
// record nothing maintained went on deciding which conversation to resume for
// days. Both failures are structural, and both are closed the same way: the
// record is keyed on the LAUNCH, not just on the orchestrator.
//
// Every launch mints a nonce, hands it to the session in the environment, and
// writes it into the durable open-set entry itself. The session's own hooks echo
// it back beside the session id they see. A record only counts as CONFIRMED --
// worth showing as this orchestrator's real, current live conversation rather
// than one nothing can vouch for -- when its nonce matches the nonce of the
// launch that entry recorded, which makes three things true by construction:
//
//   - A record from an earlier launch, or from a session that never saw this
//     launch's nonce, cannot be mistaken for the current answer.
//   - A record nobody writes any more decays on its own: the next launch mints a
//     nonce no record carries, so the store stops being confirmable the moment
//     its writer stops -- and the Manage dialog says so instead of going quiet.
//   - Neither half is authoritative alone. The desktop writes the nonce, the
//     session writes the conversation, and confirmation needs both to agree.

// orchestratorLiveConversationDirName holds one file per orchestrator naming
// the conversation that orchestrator's own session last reported being on.
const orchestratorLiveConversationDirName = "orchestrator-live"

// orchestratorLaunchEnvVar carries one launch's nonce into the session, so the
// hook that records the live conversation can stamp the record with the launch
// it belongs to. Read at run time by the hook for the same reason the
// orchestrator id is: the settings file is shared by every orchestrator, so
// nothing about one launch can be baked into it.
const orchestratorLaunchEnvVar = "ERUN_ORCHESTRATOR_LAUNCH"

// orchestratorLiveConversation is what one orchestrator's file carries: the
// conversation its session reported, and the launch that session belongs to.
type orchestratorLiveConversation struct {
	ConversationID string `json:"conversationId"`
	LaunchID       string `json:"launchId"`
	AtUnix         int64  `json:"atUnix,omitempty"`
}

// orchestratorConversationSource names why a launch resumes the conversation it
// resolved, so a caller can report the reason rather than only the id.
type orchestratorConversationSource string

const (
	// orchestratorConversationAttached is a conversation the operator chose.
	orchestratorConversationAttached orchestratorConversationSource = "attached"
	// orchestratorConversationDerived is the anchor: uuid5 of the orchestrator id.
	orchestratorConversationDerived orchestratorConversationSource = "derived"
)

// orchestratorConversationChoice is a resolved resume decision: which
// conversation, why, and what the operator has to be told about it. Notice is
// non-empty exactly when the answer is not the plain, unsurprising one -- the
// operator's own explicit attachment could not be honoured.
type orchestratorConversationChoice struct {
	ConversationID string
	Source         orchestratorConversationSource
	Notice         string
}

// orchestratorLiveConversationDir sits beside the other per-orchestrator state
// under UserConfigDir()/ERun.
func orchestratorLiveConversationDir() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = os.TempDir()
	}
	return filepath.Join(configDir, "ERun", orchestratorLiveConversationDirName)
}

// orchestratorLiveConversationPath is one orchestrator's own file. An id that is
// not a plain file name resolves to nothing: ids are slugs by construction, and
// this is what keeps a hand-edited config from naming a path elsewhere.
func orchestratorLiveConversationPath(id string) string {
	id = strings.TrimSpace(id)
	if id == "" || id != filepath.Base(id) || strings.HasPrefix(id, ".") {
		return ""
	}
	return filepath.Join(orchestratorLiveConversationDir(), id+".json")
}

// readOrchestratorLiveConversation reads what an orchestrator's own session last
// reported. Absent, unreadable, unparseable and blank all answer false: a record
// that cannot be read says nothing, and the caller falls back to the anchor.
func readOrchestratorLiveConversation(id string) (orchestratorLiveConversation, bool) {
	path := orchestratorLiveConversationPath(id)
	if path == "" {
		return orchestratorLiveConversation{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return orchestratorLiveConversation{}, false
	}
	var record orchestratorLiveConversation
	if err := json.Unmarshal(data, &record); err != nil {
		return orchestratorLiveConversation{}, false
	}
	record.ConversationID = strings.TrimSpace(record.ConversationID)
	record.LaunchID = strings.TrimSpace(record.LaunchID)
	if record.ConversationID == "" {
		return orchestratorLiveConversation{}, false
	}
	return record, true
}

// orchestratorLiveConversationForLaunch answers which conversation the session
// belonging to one specific launch is on: what that session itself reported, or
// fallback when nothing did. Used where the launch is the running one, so the
// ownership checks resolveOrchestratorConversation applies to a record from some
// earlier run do not arise -- a session writing under this launch's own nonce,
// right now, is this orchestrator's session.
func orchestratorLiveConversationForLaunch(id, launchID, fallback string) string {
	launchID = strings.TrimSpace(launchID)
	if launchID == "" {
		return fallback
	}
	record, ok := readOrchestratorLiveConversation(id)
	if !ok || record.LaunchID != launchID {
		return fallback
	}
	return record.ConversationID
}

// resolveOrchestratorConversation decides which conversation a launch of this
// orchestrator resumes, and what to say about it.
//
// Order of authority: the conversation the operator explicitly attached, else
// the anchor derived from the orchestrator id (erun#1696). The attachment has
// to clear two checks the anchor never needs -- its transcript is still on
// disk, and no other orchestrator claims it -- because the cost of resuming
// the wrong conversation is somebody else's history presented as this
// orchestrator's own. A conversation this orchestrator's own session reported
// being on (see readOrchestratorLiveConversation) is not consulted here at
// all: it is offered in the Manage dialog for the operator to attach
// deliberately (see orchestratorConversationRoles), never adopted on its own.
//
// An attachment that fails falls through to the anchor WITH a notice: an
// operator's explicit instruction that silently stopped applying is the
// failure this guards against.
func (a *App) resolveOrchestratorConversation(entry orchestratorOpenEntry) orchestratorConversationChoice {
	id := strings.TrimSpace(entry.OrchestratorID)
	if id == "" {
		// A transient (Investigate) session has no id to derive from and nothing
		// recorded under one; the caller mints a fresh conversation for it.
		return orchestratorConversationChoice{Source: orchestratorConversationDerived}
	}
	derived := orchestratorSessionID(id)
	if attached := strings.TrimSpace(entry.AttachedConversationID); attached != "" {
		claims := a.otherOrchestratorConversationClaims(id)
		if reason := orchestratorConversationUnusableReason(attached, claims); reason != "" {
			return orchestratorConversationChoice{
				ConversationID: derived,
				Source:         orchestratorConversationDerived,
				Notice:         orchestratorAttachmentUnusableNotice(id, attached, reason),
			}
		}
		return orchestratorConversationChoice{ConversationID: attached, Source: orchestratorConversationAttached}
	}
	return orchestratorConversationChoice{ConversationID: derived, Source: orchestratorConversationDerived}
}

// orchestratorTrackedUnconfirmedReason reports why a tracked record cannot be
// shown as this orchestrator's CONFIRMED live conversation in the Manage
// dialog, or "" when it can (see orchestratorConversationRoles in
// orchestrator_conversations.go, the only caller). The launch check is the
// load-bearing one: without a nonce that matches the launch the durable entry
// recorded, a record is either from a run that has since been replaced or from
// a writer that no longer exists, and neither may be presented as confirmed --
// though either can still be attached by hand, since attaching only requires
// the conversation to exist and belong to nobody else.
func orchestratorTrackedUnconfirmedReason(record orchestratorLiveConversation, entry orchestratorOpenEntry, claims map[string]string) string {
	launch := strings.TrimSpace(entry.LaunchID)
	switch {
	case launch == "":
		return "the launch that recorded it is not known"
	case record.LaunchID == "":
		return "it was recorded without naming a launch"
	case record.LaunchID != launch:
		return "it was recorded by an earlier launch, so nothing has confirmed it since"
	}
	return orchestratorConversationUnusableReason(record.ConversationID, claims)
}

// orchestratorConversationUnusableReason reports why a conversation must not be
// resumed for the orchestrator asking, or "" when it may be.
func orchestratorConversationUnusableReason(conversationID string, claims map[string]string) string {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return "it names no conversation"
	}
	if owner, taken := claims[conversationID]; taken {
		return "it belongs to orchestrator " + owner
	}
	if !orchestratorSessionExists(conversationID) {
		return "its transcript is no longer on disk"
	}
	return ""
}

// otherOrchestratorConversationClaims maps every conversation some OTHER
// configured orchestrator has a claim on to that orchestrator's id: the
// conversation derived from its id, the one it is attached to, and the one its
// own session last reported. A conversation belongs to one orchestrator, so
// this is what keeps a resume from handing over somebody else's history --
// the failure that made the previous record worse than having none.
//
// A config that cannot be read yields no claims. The guard exists to refuse a
// provable conflict, and a config it could not load is not proof of one.
func (a *App) otherOrchestratorConversationClaims(selfID string) map[string]string {
	selfID = strings.TrimSpace(selfID)
	claims := map[string]string{}
	configs, err := a.loadOrchestratorConfigs()
	if err != nil {
		return claims
	}
	entries := readOpenOrchestrators(a.deps.orchestratorOpenPath)
	for _, config := range configs {
		other := strings.TrimSpace(config.ID)
		if other == "" || other == selfID {
			continue
		}
		for _, claimed := range orchestratorClaimedConversations(other, entries) {
			claims[claimed] = other
		}
	}
	return claims
}

// orchestratorClaimedConversations lists every conversation one orchestrator has
// a claim on, in the same order resolveOrchestratorConversation weighs them.
func orchestratorClaimedConversations(id string, entries []orchestratorOpenEntry) []string {
	out := []string{orchestratorSessionID(id)}
	if attached := strings.TrimSpace(orchestratorEntryOrEmpty(entries, id).AttachedConversationID); attached != "" {
		out = append(out, attached)
	}
	if record, ok := readOrchestratorLiveConversation(id); ok {
		out = append(out, record.ConversationID)
	}
	return out
}

// orchestratorAttachmentUnusableNotice reports that the conversation the
// operator attached cannot be resumed, so this launch used the derived anchor
// instead. An attachment is an explicit instruction, so failing it quietly would
// leave the operator believing a choice they made is in force.
func orchestratorAttachmentUnusableNotice(id, attached, reason string) string {
	return fmt.Sprintf("Reopened %s without the conversation attached to it (%s): %s. "+
		"It resumed the conversation derived from its id; manage the orchestrator to attach another.",
		id, attached, reason)
}

// orchestratorLiveConversationHookCommand is the hook that keeps one
// orchestrator's live-conversation record current. It reads session_id from the
// hook's own stdin JSON -- the field every hook invocation carries, and the only
// place the id the session is actually writing to appears -- and stamps it with
// the launch nonce and orchestrator id from the environment, both resolved at
// run time because this settings file is shared by every orchestrator.
//
// Installed on the session boundaries AND the turn boundaries: a session that
// moves to a new id mid-run is exactly the case this exists for, so the record
// has to be refreshed as the session goes rather than only at its start.
//
// Runs through node, like the other orchestrator hooks, rather than a POSIX
// shell reading its own stdin with sed: this fires on every session and turn
// boundary of every orchestrator, and Windows' own hook shell (PowerShell)
// parses `[ -n ... ]` test syntax as something else entirely rather than
// executing it. Node needs no helper binary on PATH -- the AI harness that
// launched the session is itself an npm package -- and resolves identically
// regardless of the host's own hook shell. Every failure path is swallowed:
// a hook that could wedge a session costs more than a missed record.
func orchestratorLiveConversationHookCommand() string {
	dir := filepath.ToSlash(orchestratorLiveConversationDir())
	script := `let d="";process.stdin.on("data",c=>{d+=c});process.stdin.on("end",()=>{try{` +
		`const id=process.env.ERUN_ORCHESTRATOR_ID;` +
		`const launch=process.env.` + orchestratorLaunchEnvVar + `;` +
		`if(!id||!launch)return;` +
		`const j=JSON.parse(d);` +
		`const sid=j.session_id;` +
		`if(!sid)return;` +
		`const fs=require("fs");fs.mkdirSync("` + dir + `",{recursive:true});` +
		`fs.writeFileSync("` + dir + `/"+id+".json",JSON.stringify({conversationId:sid,launchId:launch,atUnix:Math.floor(Date.now()/1000)}));` +
		`}catch(e){}});`
	return `node -e '` + script + `'`
}

// isOrchestratorLiveConversationHookBlock reports whether a settings hook block
// is this recorder, so a rewrite replaces its own previous block instead of
// stacking another copy beside it. It matches on what the command does -- writes
// a conversationId stamped with a launch -- so a block is only claimed when it is
// provably ours and never because it sits nearby.
func isOrchestratorLiveConversationHookBlock(block any) bool {
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
		if strings.Contains(command, `conversationId:`) && strings.Contains(command, orchestratorLaunchEnvVar) {
			return true
		}
	}
	return false
}

// orchestratorLiveConversationHookBlock is the recorder bound to whichever event
// it is installed on.
func orchestratorLiveConversationHookBlock() []any {
	return []any{map[string]any{
		"hooks": []any{map[string]any{"type": "command", "command": orchestratorLiveConversationHookCommand()}},
	}}
}
