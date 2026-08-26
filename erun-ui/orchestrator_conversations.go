package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
)

// An operator whose orchestrator came back on the wrong conversation had exactly
// one remedy: read the transcript directory by hand, work out which file was
// growing, and write the hand-off themselves. A resume that cannot be corrected
// from inside the product is a dead end wherever it lands wrong, so this is the
// surface that makes it correctable -- what an orchestrator could resume, why
// each candidate is a candidate, and an attach that outlives the launch it was
// made on.
//
// Two rules shape what it offers. It does NOT offer another orchestrator's
// conversation: the directory holds conversations belonging to several
// orchestrators and to none, and handing one over is the crossing this whole
// area was fixed for. And it does not decide anything by recency -- the newest
// file in the directory is usually somebody else's -- so every row says whose it
// is and why.

// orchestratorConversationListCap bounds how many conversations one listing
// carries. A host accumulates transcripts indefinitely, and a picker is for
// choosing between the plausible few; the count that was dropped is reported
// rather than silently trimmed away.
const orchestratorConversationListCap = 60

// orchestratorConversationExcerptLimit bounds the excerpt one row carries.
const orchestratorConversationExcerptLimit = 160

// orchestratorConversationHeadBytes bounds how much of a transcript is read to
// recognise it. Transcripts run to megabytes and a listing reads every one of
// them, so the folder and the opening prompt are taken from the head and the
// rest is never touched.
const orchestratorConversationHeadBytes = 128 * 1024

// Roles a conversation can hold for the orchestrator being listed. They are the
// operator's actual question -- whose is this, and is it the one I would get if
// I started this orchestrator now -- so each is a distinct row label rather than
// a shade of one.
const (
	// orchestratorConversationRoleAttached is the one the operator attached.
	orchestratorConversationRoleAttached = "attached"
	// orchestratorConversationRoleLive is the one this orchestrator's own session
	// reported being on, under the launch that recorded it.
	orchestratorConversationRoleLive = "live"
	// orchestratorConversationRoleStranded is one recorded as live by a launch
	// that cannot be confirmed. It is the case that needs a decision: the work
	// may well be in there, and nothing can vouch for it.
	orchestratorConversationRoleStranded = "stranded"
	// orchestratorConversationRoleDerived is the anchor derived from the
	// orchestrator id.
	orchestratorConversationRoleDerived = "derived"
	// orchestratorConversationRoleUnowned is a conversation on this machine that
	// no orchestrator claims -- what a fork leaves behind, and what an operator
	// recovering one is usually looking for.
	orchestratorConversationRoleUnowned = "unowned"
)

// orchestratorConversation is one conversation an orchestrator could resume,
// with what it takes to choose between them: when it was last written and how
// big it is (the two facts that separate a live conversation from an abandoned
// one), where it was started, and how it opens.
type orchestratorConversation struct {
	ConversationID  string `json:"conversationId"`
	Folder          string `json:"folder"`
	LastWrittenUnix int64  `json:"lastWrittenUnix"`
	SizeBytes       int64  `json:"sizeBytes"`
	Excerpt         string `json:"excerpt,omitempty"`
	Role            string `json:"role"`
	// Resuming marks the row a launch of this orchestrator would resume right
	// now, so the operator can see whether attaching would change anything.
	Resuming bool `json:"resuming"`
}

// orchestratorConversations is the listing: which conversation this orchestrator
// resumes today and why, every conversation it could be pointed at instead, and
// whatever the resolution had to say about the current answer.
type orchestratorConversations struct {
	OrchestratorID  string                     `json:"orchestratorId"`
	Resuming        string                     `json:"resuming"`
	ResumingSource  string                     `json:"resumingSource"`
	Attached        string                     `json:"attached,omitempty"`
	Notice          string                     `json:"notice,omitempty"`
	Conversations   []orchestratorConversation `json:"conversations"`
	OmittedForCap   int                        `json:"omittedForCap,omitempty"`
	OmittedNotMine  int                        `json:"omittedNotMine,omitempty"`
	TranscriptsRoot string                     `json:"transcriptsRoot"`
}

// ListOrchestratorConversations reports the conversations one orchestrator could
// resume. Conversations another orchestrator has a claim on are left out and
// counted, so a short list reads as "these are yours" rather than as an empty
// machine.
func (a *App) ListOrchestratorConversations(id string) (orchestratorConversations, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return orchestratorConversations{}, fmt.Errorf("no orchestrator named")
	}
	if _, err := a.findOrchestratorConfig(id); err != nil {
		return orchestratorConversations{}, err
	}
	entry := orchestratorEntryOrEmpty(readOpenOrchestrators(a.deps.orchestratorOpenPath), id)
	choice := a.resolveOrchestratorConversation(entry)
	out := orchestratorConversations{
		OrchestratorID:  id,
		Resuming:        choice.ConversationID,
		ResumingSource:  string(choice.Source),
		Attached:        strings.TrimSpace(entry.AttachedConversationID),
		Notice:          choice.Notice,
		Conversations:   []orchestratorConversation{},
		TranscriptsRoot: orchestratorTranscriptsRoot(),
	}
	claims := a.otherOrchestratorConversationClaims(id)
	roles := a.orchestratorConversationRoles(id, entry, choice)
	files, err := orchestratorTranscriptFiles()
	if err != nil {
		return out, err
	}
	for _, file := range files {
		conversationID := strings.TrimSuffix(filepath.Base(file), ".jsonl")
		if _, taken := claims[conversationID]; taken {
			out.OmittedNotMine++
			continue
		}
		row := readOrchestratorConversation(file, conversationID)
		row.Role = orchestratorConversationRoleUnowned
		if role, ok := roles[conversationID]; ok {
			row.Role = role
		}
		row.Resuming = conversationID == choice.ConversationID
		out.Conversations = append(out.Conversations, row)
	}
	// The derived anchor is always a choice, even before anything has been
	// written to it: it is where a launch lands when nothing else resolves, so a
	// list that omitted it could not explain what the operator is looking at.
	out.Conversations = withOrchestratorAnchorRow(out.Conversations, orchestratorSessionID(id), choice, roles)
	sort.Slice(out.Conversations, func(i, j int) bool {
		if out.Conversations[i].LastWrittenUnix != out.Conversations[j].LastWrittenUnix {
			return out.Conversations[i].LastWrittenUnix > out.Conversations[j].LastWrittenUnix
		}
		return out.Conversations[i].ConversationID < out.Conversations[j].ConversationID
	})
	if len(out.Conversations) > orchestratorConversationListCap {
		out.OmittedForCap = len(out.Conversations) - orchestratorConversationListCap
		out.Conversations = out.Conversations[:orchestratorConversationListCap]
	}
	return out, nil
}

// orchestratorConversationRoles maps the conversations this orchestrator has a
// relationship with to what that relationship is.
func (a *App) orchestratorConversationRoles(id string, entry orchestratorOpenEntry, choice orchestratorConversationChoice) map[string]string {
	roles := map[string]string{orchestratorSessionID(id): orchestratorConversationRoleDerived}
	if record, ok := readOrchestratorLiveConversation(id); ok {
		// The same record reads as live or as stranded depending on whether the
		// resolution could stand behind it, which is exactly the distinction an
		// operator staring at two transcripts needs drawn for them.
		role := orchestratorConversationRoleStranded
		if choice.Source == orchestratorConversationTracked && choice.ConversationID == record.ConversationID {
			role = orchestratorConversationRoleLive
		}
		roles[record.ConversationID] = role
	}
	if attached := strings.TrimSpace(entry.AttachedConversationID); attached != "" {
		roles[attached] = orchestratorConversationRoleAttached
	}
	return roles
}

// withOrchestratorAnchorRow adds the derived conversation as a row when no
// transcript for it was found, so the anchor is visible before its first write.
func withOrchestratorAnchorRow(rows []orchestratorConversation, derived string, choice orchestratorConversationChoice, roles map[string]string) []orchestratorConversation {
	for _, row := range rows {
		if row.ConversationID == derived {
			return rows
		}
	}
	role := orchestratorConversationRoleDerived
	if named, ok := roles[derived]; ok {
		role = named
	}
	return append(rows, orchestratorConversation{
		ConversationID: derived,
		Role:           role,
		Resuming:       derived == choice.ConversationID,
	})
}

// AttachOrchestratorConversation points an orchestrator at a conversation the
// operator chose and starts it there, replacing any session it already has. The
// choice is durable: recomputing it away on the next launch would make the
// attach a gesture rather than a decision.
//
// A conversation another orchestrator claims is refused by name. Recovering a
// mis-attached orchestrator is what this exists for, and handing one
// orchestrator another's history is the failure it exists to prevent -- so the
// refusal says who owns it, which is the operator's way to reach the same
// conversation from the orchestrator it belongs to.
func (a *App) AttachOrchestratorConversation(id, conversationID string, cols, rows int) (orchestratorInfo, error) {
	id = strings.TrimSpace(id)
	conversationID = strings.TrimSpace(conversationID)
	def, err := a.findOrchestratorConfig(id)
	if err != nil {
		return orchestratorInfo{}, err
	}
	if conversationID == "" {
		return orchestratorInfo{}, fmt.Errorf("no conversation named to attach %s to", id)
	}
	if reason := orchestratorConversationUnusableReason(conversationID, a.otherOrchestratorConversationClaims(id)); reason != "" {
		return orchestratorInfo{}, fmt.Errorf("cannot attach %s to conversation %s: %s", id, conversationID, reason)
	}
	return a.restartOrchestratorOnConversation(def, conversationID, conversationID, cols, rows)
}

// DetachOrchestratorConversation drops an explicit attachment and restarts the
// orchestrator on whatever it would resolve to on its own -- what it is tracked
// as live on, or the derived anchor. An attachment the operator could set and
// not clear would be a trap rather than a correction.
func (a *App) DetachOrchestratorConversation(id string, cols, rows int) (orchestratorInfo, error) {
	id = strings.TrimSpace(id)
	def, err := a.findOrchestratorConfig(id)
	if err != nil {
		return orchestratorInfo{}, err
	}
	return a.restartOrchestratorOnConversation(def, "", "", cols, rows)
}

// restartOrchestratorOnConversation replaces an orchestrator's session with one
// on the given conversation ("" to let the launch resolve it), recording attach
// as the operator's standing choice ("" clears it). The teardown comes first
// because a running session holds the terminal and would otherwise be handed
// back unchanged -- and the choice is written after it, because stopping an
// orchestrator is also how the operator says it should not come back, so it
// forgets the durable entry the choice lives on.
func (a *App) restartOrchestratorOnConversation(def eruncommon.OrchestratorConfig, conversationID, attach string, cols, rows int) (orchestratorInfo, error) {
	a.stopOrchestratorSession(def.ID)
	if err := setAttachedOrchestratorConversation(a.deps.orchestratorOpenPath, def.ID, attach); err != nil {
		return orchestratorInfo{}, fmt.Errorf("record the attached conversation: %w", err)
	}
	return a.spawnOrchestratorSession(orchestratorSpawn{
		id:             def.ID,
		name:           def.Name,
		envs:           a.refreshLinkedEnvDirectories(def.Environments),
		conversationID: conversationID,
		cols:           cols,
		rows:           rows,
	})
}

// orchestratorTranscriptsRoot is the directory the harness keeps its transcripts
// under, one subdirectory per working directory.
func orchestratorTranscriptsRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}

// orchestratorTranscriptFiles lists every transcript on this machine, across
// every working directory the harness has been started in. Cross-directory by
// design: a conversation id is globally unique, and an orchestrator's own
// conversation can perfectly well have been started somewhere else.
func orchestratorTranscriptFiles() ([]string, error) {
	root := orchestratorTranscriptsRoot()
	if root == "" {
		return nil, nil
	}
	return filepath.Glob(filepath.Join(root, "*", "*.jsonl"))
}

// readOrchestratorConversation describes one transcript. Everything it reports
// beyond the file's own stat comes from the head of the file, so a listing over
// a directory of multi-megabyte transcripts stays cheap.
func readOrchestratorConversation(path, conversationID string) orchestratorConversation {
	row := orchestratorConversation{ConversationID: conversationID}
	if info, err := os.Stat(path); err == nil {
		row.LastWrittenUnix = info.ModTime().Unix()
		row.SizeBytes = info.Size()
	}
	// The folder is read from the transcript's own recorded cwd, never decoded
	// from the directory name: the harness encodes a path by replacing every
	// separator with a dash, which no decode can round-trip for a path that
	// already contained one.
	row.Folder, row.Excerpt = readOrchestratorTranscriptHead(path)
	if row.Folder == "" {
		row.Folder = filepath.Base(filepath.Dir(path))
	}
	return row
}

// readOrchestratorTranscriptHead reads the working directory and the opening
// prompt out of a transcript's first records.
func readOrchestratorTranscriptHead(path string) (folder, excerpt string) {
	file, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), orchestratorConversationHeadBytes)
	read := 0
	for scanner.Scan() {
		read += len(scanner.Bytes())
		if read > orchestratorConversationHeadBytes {
			break
		}
		var record struct {
			Type    string          `json:"type"`
			CWD     string          `json:"cwd"`
			Content string          `json:"content"`
			Message json.RawMessage `json:"message"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			continue
		}
		if folder == "" {
			folder = strings.TrimSpace(record.CWD)
		}
		if excerpt == "" {
			excerpt = orchestratorConversationExcerpt(record.Type, record.Content, record.Message)
		}
		if folder != "" && excerpt != "" {
			break
		}
	}
	return folder, excerpt
}

// orchestratorConversationExcerpt renders the first thing a human said in a
// conversation, which is how they recognise it. The harness writes a user
// message either as a plain string or as content blocks, and the very first
// record is often the queued prompt rather than a message at all, so all three
// shapes are read.
func orchestratorConversationExcerpt(recordType, content string, message json.RawMessage) string {
	if recordType != "user" && recordType != "queue-operation" {
		return ""
	}
	if text := orchestratorConversationOneLine(content); text != "" {
		return text
	}
	var decoded struct {
		Content json.RawMessage `json:"content"`
	}
	if len(message) == 0 || json.Unmarshal(message, &decoded) != nil {
		return ""
	}
	var plain string
	if json.Unmarshal(decoded.Content, &plain) == nil {
		return orchestratorConversationOneLine(plain)
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(decoded.Content, &blocks) != nil {
		return ""
	}
	for _, block := range blocks {
		if text := orchestratorConversationOneLine(block.Text); text != "" {
			return text
		}
	}
	return ""
}

// orchestratorConversationOneLine collapses text to one bounded line, since it
// renders in a table row.
func orchestratorConversationOneLine(text string) string {
	text = strings.TrimSpace(strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ").Replace(text))
	for strings.Contains(text, "  ") {
		text = strings.ReplaceAll(text, "  ", " ")
	}
	if len(text) > orchestratorConversationExcerptLimit {
		return strings.TrimSpace(text[:orchestratorConversationExcerptLimit]) + "…"
	}
	return text
}
