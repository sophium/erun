// Package contribute owns the desktop's contribute mode: the per-environment
// flag, the ERun clone it needs, the contribute terminal tabs, and the in-pod
// headless app forwarded to the host.
package contribute

import (
	"context"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// Store is the slice of the desktop's config store the contribute concern reads.
type Store interface {
	config.OpenStore
}

// Terminal session kinds of the contribute tabs, as the terminal names them.
const (
	KindERun = "contribute-erun"
	KindAI   = "contribute-ai"
)

// Readiness is a started session the action runner's gate waits on.
type Readiness interface {
	WaitReady(ctx context.Context, timeout time.Duration) error
}

// Launch is one contribute tab to spawn in the operator's erun clone.
type Launch struct {
	Selection   uimodel.Selection
	Key         string
	Slot        int
	Kind        string
	AppSession  string
	WithAI      bool
	Tenant      string
	Environment string
	RepoPath    string
	Cols        int
	Rows        int
	TabName     string
}

// Reused is a live contribute tab a start hands back instead of spawning.
type Reused struct {
	Serial    int
	Selection uimodel.Selection
	Ready     Readiness
}

// Sessions is the terminal session registry as the contribute concern reaches
// it. Each call takes the terminal lock itself, so none may be made while a
// contribute lock is held.
type Sessions interface {
	// Reuse returns the open session registered under key, already signalled ready.
	Reuse(key string) (Reused, bool)
	// Spawn starts and registers the tab; a nil Readiness on error.
	Spawn(launch Launch) (int, Readiness, error)
	// CloseWithKeyPrefix closes and forgets every session whose key has one of prefixes.
	CloseWithKeyPrefix(prefixes ...string)
	// WriteToFirstWithKeyPrefix writes input to one open session whose key has prefix.
	WriteToFirstWithKeyPrefix(prefix, input string)
}

type Deps struct {
	Store Store
	// Context is read per call: the Wails context arrives after the service is built.
	Context  func() context.Context
	Emit     uimodel.Emitter
	Sessions Sessions

	MCPEndpoint func(config.OpenResult) string
	MCPBearer   func(tenant, environment string) string
	// CanReachMCPEndpoint checks traffic, not a dial: a stale port-forward accepts and never answers.
	CanReachMCPEndpoint func(port int) bool
	WrapMCPUnreachable  func(error) error
	IsMCPDialFailure    func(error) bool
	CloneERun           func(ctx context.Context, endpoint, bearer string) error

	// StatePath is loaded once at construction; "" keeps the flags in memory only.
	StatePath string
}

type Service struct {
	d        Deps
	flags    *flagStore
	forwards *appForwards
}

// New panics on a missing seam rather than defaulting it, so a test can never
// reach a real process or network call it did not wire.
func New(d Deps) *Service {
	if missing := missingSeams(d); len(missing) > 0 {
		panic("contribute.New: missing " + strings.Join(missing, ", "))
	}
	return &Service{d: d, flags: newFlagStore(d.StatePath), forwards: newAppForwards()}
}

func missingSeams(d Deps) []string {
	required := []struct {
		name    string
		missing bool
	}{
		{"Store", d.Store == nil},
		{"Context", d.Context == nil},
		{"Emit", d.Emit == nil},
		{"Sessions", d.Sessions == nil},
		{"MCPEndpoint", d.MCPEndpoint == nil},
		{"MCPBearer", d.MCPBearer == nil},
		{"CanReachMCPEndpoint", d.CanReachMCPEndpoint == nil},
		{"WrapMCPUnreachable", d.WrapMCPUnreachable == nil},
		{"IsMCPDialFailure", d.IsMCPDialFailure == nil},
		{"CloneERun", d.CloneERun == nil},
	}
	var missing []string
	for _, seam := range required {
		if seam.missing {
			missing = append(missing, seam.name)
		}
	}
	return missing
}
