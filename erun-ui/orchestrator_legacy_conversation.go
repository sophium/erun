package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// Before orchestrators ran as background agents, the desktop chose which
// conversation each launch resumed: one the operator attached, else what the
// orchestrator's own session last reported, else a conversation id derived
// from the orchestrator id. An orchestrator created under that scheme still has
// its work in one of those conversations, so the first agent created for it
// continues that conversation instead of starting empty. Nothing here is
// written any more; it is read once, when an orchestrator has no agent yet.

// legacyOrchestratorSessionNamespace seeded the derived conversation id.
var legacyOrchestratorSessionNamespace = uuid.MustParse("6f7e9c2a-1b3d-4e5f-8a9b-0c1d2e3f4a5b")

// orchestratorLegacyConversation returns the conversation an orchestrator was
// on under the previous scheme, or "" when none of them is still on disk.
func orchestratorLegacyConversation(openPath, id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	candidates := []string{
		legacyAttachedConversation(openPath, id),
		legacyReportedConversation(id),
		uuid.NewSHA1(legacyOrchestratorSessionNamespace, []byte(id)).String(),
	}
	for _, candidate := range candidates {
		if candidate != "" && claudeConversationExists(candidate) {
			return candidate
		}
	}
	return ""
}

// legacyAttachedConversation reads the conversation the operator attached by
// hand, kept in the open-set record under the previous scheme.
func legacyAttachedConversation(openPath, id string) string {
	data, err := os.ReadFile(openPath)
	if err != nil {
		return ""
	}
	var state struct {
		Orchestrators []struct {
			OrchestratorID         string `json:"orchestratorId"`
			AttachedConversationID string `json:"attachedConversationId"`
		} `json:"orchestrators"`
	}
	if json.Unmarshal(data, &state) != nil {
		return ""
	}
	for _, entry := range state.Orchestrators {
		if strings.TrimSpace(entry.OrchestratorID) == id {
			return strings.TrimSpace(entry.AttachedConversationID)
		}
	}
	return ""
}

// legacyReportedConversation reads what the orchestrator's own session last
// reported being on, from the record its hooks used to write.
func legacyReportedConversation(id string) string {
	if id != filepath.Base(id) || strings.HasPrefix(id, ".") {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(orchestratorStateDir(), "orchestrator-live", id+".json"))
	if err != nil {
		return ""
	}
	var record struct {
		ConversationID string `json:"conversationId"`
	}
	if json.Unmarshal(data, &record) != nil {
		return ""
	}
	return strings.TrimSpace(record.ConversationID)
}

// claudeConversationExists reports whether a Claude conversation transcript
// with this id is on disk, in any project folder.
func claudeConversationExists(conversationID string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	matches, err := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", conversationID+".jsonl"))
	return err == nil && len(matches) > 0
}
