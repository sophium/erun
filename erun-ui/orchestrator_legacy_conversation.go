package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// Orchestrators created before background agents kept their work in a
// desktop-chosen conversation; their first agent continues it. Read-only.

// legacyOrchestratorSessionNamespace derives legacy conversation ids; never change it.
var legacyOrchestratorSessionNamespace = uuid.MustParse("6f7e9c2a-1b3d-4e5f-8a9b-0c1d2e3f4a5b")

// orchestratorLegacyConversation returns "" when no legacy conversation is on disk.
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

func claudeConversationExists(conversationID string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	matches, err := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", conversationID+".jsonl"))
	return err == nil && len(matches) > 0
}
