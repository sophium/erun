// Package cloudctx owns the desktop's live view of cloud contexts: the power
// status cache and its background poller, and the per-environment refreshers
// that push temporary host AWS credentials into a running environment.
package cloudctx

import (
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/cloud/cloudcontext"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// Store is the slice of the desktop's config store the cache and refreshers read.
type Store interface {
	config.OpenStore
}

type Deps struct {
	Store       Store
	Cloud       cloud.CloudDependencies
	ContextDeps cloudcontext.CloudContextDependencies
	// MCPBearer signs the per-environment bearer the credential calls present.
	MCPBearer func(tenant, environment string) string
	// MCPReachable checks traffic, not a dial: a stale port-forward accepts and never answers.
	MCPReachable  func(port int) bool
	MCPEndpoint   func(config.OpenResult) string
	MCPTransport  func(endpoint, bearer string) mcp.Transport
	ClientVersion func() string
	// NotifyRefreshFailure posts the env-scoped error a failing refresher raises once per episode.
	NotifyRefreshFailure func(selection uimodel.Selection, message string)
	// OnStatusChange republishes whatever the desktop derives from the cache.
	// It runs after the cache lock is released, because it reads the cache back.
	OnStatusChange func()
	// Now is the clock the cache ages entries by; nil means time.Now.
	Now func() time.Time
}

type Service struct {
	d Deps

	// statusMu is a leaf: nothing is called out while it is held.
	statusMu sync.RWMutex
	statuses map[string]cacheEntry

	// mu guards the poller and the refreshers; it is a leaf.
	mu         sync.Mutex
	pollerStop chan struct{}
	refreshers map[string]*refresher
}

// New panics on a missing seam rather than defaulting it, so a test can never
// reach a real process or network call it did not wire.
func New(d Deps) *Service {
	if d.Store == nil || d.MCPBearer == nil || d.MCPReachable == nil || d.MCPEndpoint == nil ||
		d.MCPTransport == nil || d.ClientVersion == nil || d.NotifyRefreshFailure == nil || d.OnStatusChange == nil {
		panic("cloudctx.New: Store, MCPBearer, MCPReachable, MCPEndpoint, MCPTransport, ClientVersion, NotifyRefreshFailure and OnStatusChange are required")
	}
	return &Service{
		d:          d,
		statuses:   make(map[string]cacheEntry),
		refreshers: make(map[string]*refresher),
	}
}

func (s *Service) now() time.Time {
	if s.d.Now != nil {
		return s.d.Now()
	}
	return time.Now()
}
