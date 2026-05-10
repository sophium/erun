package eruncommon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// RunningCommand is the on-disk representation of an in-flight long-running
// erun command. Every mutating top-level command writes one of these into
// `<config-dir>/commands/<id>.json` at start and removes it at completion
// (success or failure). The desktop watches the directory to render a live
// "what is running" view that covers every caller — CLI, MCP, scripted CI,
// desktop button, manually-typed `erun deploy/build/release/open` in any
// tab — without scraping PTY output.
//
// Command-specific fields are optional: deploys carry release/namespace/
// context, builds carry component/image, releases carry version. The
// Summary field is a free-form one-line description rendered when no
// command-specific renderer applies.
type RunningCommand struct {
	ID                string    `json:"id"`
	Command           string    `json:"command"`
	PID               int       `json:"pid"`
	StartedAt         time.Time `json:"started_at"`
	Tenant            string    `json:"tenant,omitempty"`
	Environment       string    `json:"environment,omitempty"`
	Version           string    `json:"version,omitempty"`
	Release           string    `json:"release,omitempty"`
	Namespace         string    `json:"namespace,omitempty"`
	KubernetesContext string    `json:"kubernetes_context,omitempty"`
	Component         string    `json:"component,omitempty"`
	Image             string    `json:"image,omitempty"`
	Summary           string    `json:"summary,omitempty"`
	ParamsHash        string    `json:"params_hash,omitempty"`
}

// RunningCommandHandle owns an on-disk command marker. Callers must
// Release() when the underlying command finishes; Release is safe on a nil
// handle so callers can defer it unconditionally.
type RunningCommandHandle struct {
	path string
}

// Release removes the marker so subsequent runs and the desktop watcher
// see the command as completed.
func (h *RunningCommandHandle) Release() {
	if h == nil || h.path == "" {
		return
	}
	_ = os.Remove(h.path)
}

// Path returns the on-disk path of the marker. Empty when the handle is
// nil. Useful for debugging and tests.
func (h *RunningCommandHandle) Path() string {
	if h == nil {
		return ""
	}
	return h.path
}

const runningCommandDirName = "commands"

type runningCommandDeps struct {
	configDir func() (string, error)
	now       func() time.Time
	pid       func() int
}

func (d runningCommandDeps) resolved() runningCommandDeps {
	if d.configDir == nil {
		d.configDir = ERunConfigDir
	}
	if d.now == nil {
		d.now = time.Now
	}
	if d.pid == nil {
		d.pid = os.Getpid
	}
	return d
}

// RegisterRunningCommand writes a fresh marker for the supplied command and
// returns a handle that releases it on Release(). In dry-run mode no disk
// state is created and a nil handle is returned. Failures to write the
// marker do not block the caller — they degrade desktop visibility, not
// command execution.
func RegisterRunningCommand(ctx Context, record RunningCommand) (*RunningCommandHandle, error) {
	return registerRunningCommand(ctx, record, runningCommandDeps{})
}

func registerRunningCommand(ctx Context, record RunningCommand, deps runningCommandDeps) (*RunningCommandHandle, error) {
	if ctx.DryRun {
		return nil, nil
	}
	deps = deps.resolved()
	if strings.TrimSpace(record.Command) == "" {
		return nil, fmt.Errorf("running-command record requires a command")
	}
	configDir, err := deps.configDir()
	if err != nil || strings.TrimSpace(configDir) == "" {
		ctx.Trace("running-command: skip register (config dir unavailable)")
		return nil, nil
	}
	dir := filepath.Join(configDir, runningCommandDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		ctx.Trace("running-command: skip register (mkdir failed: " + err.Error() + ")")
		return nil, nil
	}
	if record.StartedAt.IsZero() {
		record.StartedAt = deps.now().UTC()
	} else {
		record.StartedAt = record.StartedAt.UTC()
	}
	if record.PID == 0 {
		record.PID = deps.pid()
	}
	if strings.TrimSpace(record.ID) == "" {
		record.ID = generateRunningCommandID(record)
	}
	path := filepath.Join(dir, sanitizeForFilename(record.ID)+".json")
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode running-command record: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return nil, fmt.Errorf("write running-command marker: %w", err)
	}
	ctx.Trace(fmt.Sprintf("running-command: register command=%s id=%s", record.Command, record.ID))
	return &RunningCommandHandle{path: path}, nil
}

// ListRunningCommands returns every readable marker in the configured
// commands directory, newest first. Unreadable or stale-pid entries are
// included as-is so callers (the desktop watcher) can decide how to
// surface them.
func ListRunningCommands() ([]RunningCommand, error) {
	return listRunningCommands(runningCommandDeps{}.resolved())
}

func listRunningCommands(deps runningCommandDeps) ([]RunningCommand, error) {
	configDir, err := deps.configDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(configDir, runningCommandDirName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]RunningCommand, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		var record RunningCommand
		if err := json.Unmarshal(data, &record); err != nil {
			continue
		}
		out = append(out, record)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].StartedAt.After(out[j].StartedAt)
	})
	return out, nil
}

// RunningCommandsDirPath returns the absolute path of the commands
// directory rooted at the user config dir. Used by the desktop to set up
// its filesystem watcher.
func RunningCommandsDirPath() (string, error) {
	configDir, err := ERunConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, runningCommandDirName), nil
}

func generateRunningCommandID(r RunningCommand) string {
	parts := []string{strings.TrimSpace(r.Command)}
	if r.Tenant != "" {
		parts = append(parts, r.Tenant)
	}
	if r.Environment != "" {
		parts = append(parts, r.Environment)
	}
	if r.Component != "" {
		parts = append(parts, r.Component)
	}
	if r.Release != "" && r.Namespace != "" {
		parts = append(parts, r.Namespace, r.Release)
	}
	parts = append(parts, fmt.Sprintf("%d", r.StartedAt.UnixNano()))
	return strings.Join(parts, "-")
}
