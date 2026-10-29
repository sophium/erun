package orchestrator

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

type fakeClaudeAgents struct {
	mu        sync.Mutex
	agents    []ClaudeAgent
	creates   [][]string
	stops     []string
	removes   []string
	next      int
	createErr error
}

func newFakeClaudeAgents(agents ...ClaudeAgent) *fakeClaudeAgents {
	return &fakeClaudeAgents{agents: agents}
}

func (f *fakeClaudeAgents) List() ([]ClaudeAgent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ClaudeAgent(nil), f.agents...), nil
}

func (f *fakeClaudeAgents) Create(dir string, args []string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.creates = append(f.creates, append([]string(nil), args...))
	if f.createErr != nil {
		return "", f.createErr
	}
	f.next++
	id := fmt.Sprintf("%08x", 0xa0000000+f.next)
	agent := ClaudeAgent{ID: id, PID: 1000 + f.next, Cwd: dir, Kind: "background", SessionID: id + "-0000-4000-8000-000000000000"}
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "-n":
			agent.Name = args[i+1]
		case "--resume":
			agent.SessionID = args[i+1]
		}
	}
	f.agents = append(f.agents, agent)
	return id, nil
}

func (f *fakeClaudeAgents) Stop(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops = append(f.stops, id)
	for i := range f.agents {
		if f.agents[i].ID == id {
			f.agents[i].PID = 0
			return nil
		}
	}
	return errors.New("no agent " + id)
}

func (f *fakeClaudeAgents) Remove(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removes = append(f.removes, id)
	kept := f.agents[:0]
	for _, agent := range f.agents {
		if agent.ID != id {
			kept = append(kept, agent)
		}
	}
	f.agents = kept
	return nil
}

func (f *fakeClaudeAgents) clear(id, sessionID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.agents {
		if f.agents[i].ID == id {
			f.agents[i].SessionID = sessionID
		}
	}
}

func (f *fakeClaudeAgents) named(name string) []ClaudeAgent {
	agents, _ := f.List()
	return findOrchestratorAgents(agents, name)
}

func fakeAgentsOf(t *testing.T, app *testApp) *fakeClaudeAgents {
	t.Helper()
	fake, ok := app.deps.claudeAgents.(*fakeClaudeAgents)
	if !ok {
		t.Fatalf("expected the app to drive a fake Claude agent service, got %T", app.deps.claudeAgents)
	}
	return fake
}

func stageOrchestratorConversation(t *testing.T, conversationID string) {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("resolve home: %v", err)
	}
	dir := filepath.Join(home, ".claude", "projects", "-orchestrators")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create transcript dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, conversationID+".jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
}
