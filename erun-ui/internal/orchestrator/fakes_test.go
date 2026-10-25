package orchestrator

import (
	"io"
	"sync"
	"time"

	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// lockTerminalForTest holds the terminal lock and the service's own until
// unlock runs, so a test can read orchestrator state across several statements.
func lockTerminalForTest(app *testApp) (*fakeTerminals, func()) {
	app.terminals.mu.Lock()
	app.mu.Lock()
	return app.terminals, func() {
		app.mu.Unlock()
		app.terminals.mu.Unlock()
	}
}

// fakeTerminals is the terminal registry, keyed by orchestrator id.
type fakeTerminals struct {
	mu         sync.Mutex
	sessions   map[string]*fakeTerminal
	nextSerial int
}

func newFakeTerminals() *fakeTerminals {
	return &fakeTerminals{sessions: map[string]*fakeTerminal{}}
}

func (f *fakeTerminals) WithLock(fn func(Registry)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(fakeRegistry{f: f})
}

// seed registers pty as id's open session under serial, as a test that skips
// the spawn path needs.
func (f *fakeTerminals) seed(id string, serial int, pty PTY) *fakeTerminal {
	f.mu.Lock()
	defer f.mu.Unlock()
	if serial > f.nextSerial {
		f.nextSerial = serial
	}
	session := &fakeTerminal{owner: f, id: id, serial: serial, pty: pty}
	f.sessions[id] = session
	return session
}

func (f *fakeTerminals) closeAll() {
	f.mu.Lock()
	sessions := make([]*fakeTerminal, 0, len(f.sessions))
	for _, session := range f.sessions {
		sessions = append(sessions, session)
	}
	f.sessions = map[string]*fakeTerminal{}
	f.mu.Unlock()
	for _, session := range sessions {
		session.Close()
	}
}

type fakeRegistry struct {
	f *fakeTerminals
}

func (r fakeRegistry) Live(id string) (Terminal, bool) {
	session := r.f.sessions[id]
	if session == nil || session.closed {
		return nil, false
	}
	return session, true
}

func (r fakeRegistry) Register(id string, pty PTY, respawn func() (PTY, error)) Terminal {
	r.f.nextSerial++
	session := &fakeTerminal{owner: r.f, id: id, serial: r.f.nextSerial, pty: pty, respawn: respawn}
	r.f.sessions[id] = session
	return session
}

func (r fakeRegistry) Remove(id string) (Terminal, bool) {
	session := r.f.sessions[id]
	if session == nil {
		return nil, false
	}
	delete(r.f.sessions, id)
	return session, true
}

func (r fakeRegistry) TypedRecently(id string) bool {
	session := r.f.sessions[id]
	return session != nil && !session.lastInputAt.IsZero() && time.Since(session.lastInputAt) < 2*time.Second
}

// fakeTerminal is one registered session; its fields are guarded by its owner's lock.
type fakeTerminal struct {
	owner       *fakeTerminals
	id          string
	serial      int
	pty         PTY
	closed      bool
	respawn     func() (PTY, error)
	lastInputAt time.Time
}

func (t *fakeTerminal) Serial() int { return t.serial }

func (t *fakeTerminal) Stream() {}

func (t *fakeTerminal) Close() {
	t.owner.mu.Lock()
	t.closed = true
	pty := t.pty
	t.owner.mu.Unlock()
	if pty != nil {
		_ = pty.Close()
	}
}

func (t *fakeTerminal) WriteIfOpen(text string) bool {
	t.owner.mu.Lock()
	var pty PTY
	if !t.closed {
		pty = t.pty
	}
	t.owner.mu.Unlock()
	if pty == nil {
		return false
	}
	_, err := io.WriteString(pty, text)
	return err == nil
}

// typed records operator input into the session now.
func (t *fakeTerminal) typed() {
	t.owner.mu.Lock()
	t.lastInputAt = time.Now()
	t.owner.mu.Unlock()
}

// activityState mirrors the runtime view's observation, so a test states it the same way.
type activityState struct {
	Reachable               bool
	Observed                bool
	Busy                    bool
	Outage                  bool
	CheckFailed             bool
	Detail                  string
	BusyHolderOrchestrators string
}

func (state activityState) envActivity() EnvActivity {
	return EnvActivity{
		Snapshot: uimodel.ActivitySnapshot{
			Reachable:   state.Reachable,
			Observed:    state.Observed,
			Outage:      state.Outage,
			CheckFailed: state.CheckFailed,
			Busy:        state.Busy,
			Detail:      state.Detail,
		},
		BusyHolderOrchestrators: state.BusyHolderOrchestrators,
	}
}

// envActivities states observations keyed by selection the way the runtime snapshot holds them.
func envActivities(states map[string]activityState) map[string]EnvActivity {
	out := make(map[string]EnvActivity, len(states))
	for key, state := range states {
		out[key] = state.envActivity()
	}
	return out
}

// fakeRuntime is the runtime view's readings, keyed by selection.
type fakeRuntime struct {
	mu       sync.Mutex
	activity map[string]EnvActivity
	usage    map[string]uimodel.UsageSnapshot
}

func (r *fakeRuntime) PublishActivity(selection uimodel.Selection, state activityState) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.activity == nil {
		r.activity = map[string]EnvActivity{}
	}
	r.activity[uimodel.SelectionKey(uimodel.NormalizeSelection(selection))] = state.envActivity()
}

func (r *fakeRuntime) PublishUsage(selection uimodel.Selection, usage uimodel.UsageSnapshot) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.usage == nil {
		r.usage = map[string]uimodel.UsageSnapshot{}
	}
	r.usage[uimodel.SelectionKey(uimodel.NormalizeSelection(selection))] = usage
}

func (r *fakeRuntime) activitySnapshot() map[string]EnvActivity {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.activity) == 0 {
		return nil
	}
	out := make(map[string]EnvActivity, len(r.activity))
	for key, value := range r.activity {
		out[key] = value
	}
	return out
}

func (r *fakeRuntime) ActivitySnapshot() map[string]EnvActivity { return r.activitySnapshot() }

func (r *fakeRuntime) usageSnapshot() map[string]uimodel.UsageSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.usage) == 0 {
		return nil
	}
	out := make(map[string]uimodel.UsageSnapshot, len(r.usage))
	for key, value := range r.usage {
		out[key] = value
	}
	return out
}
