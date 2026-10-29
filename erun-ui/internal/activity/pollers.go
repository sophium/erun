package activity

import (
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/lifecycle/list"
)

const activityPollerInterval = 5 * time.Second

// StartPollers starts the helm release poller and the stale-shell detector on a
// fresh stop channel, which it returns so the caller can run its own pollers on
// it; false when they already run.
func (s *Service) StartPollers() (<-chan struct{}, bool) {
	s.pollersMu.Lock()
	if s.pollersStop != nil {
		s.pollersMu.Unlock()
		return nil, false
	}
	stop := make(chan struct{})
	s.pollersStop = stop
	s.pollersMu.Unlock()

	s.seedConfiguredKubeContexts()

	go s.runHelmReleasePoller(stop)
	go s.runStaleShellDetector(stop)
	return stop, true
}

// StopPollers closes the stop channel StartPollers handed out; it is idempotent.
func (s *Service) StopPollers() {
	s.pollersMu.Lock()
	stop := s.pollersStop
	s.pollersStop = nil
	s.pollersMu.Unlock()
	if stop != nil {
		close(stop)
	}
}

// seedConfiguredKubeContexts surfaces deploys left pending by a previous run;
// store errors are non-fatal.
func (s *Service) seedConfiguredKubeContexts() {
	result, err := list.ResolveListResult(s.d.Store, s.d.FindProjectRoot, model.OpenParams{})
	if err != nil {
		return
	}
	s.watchedContextsMu.Lock()
	defer s.watchedContextsMu.Unlock()
	if s.watchedContexts == nil {
		s.watchedContexts = make(map[string]struct{})
	}
	for _, tenant := range result.Tenants {
		for _, env := range tenant.Environments {
			if name := strings.TrimSpace(env.KubernetesContext); name != "" {
				s.watchedContexts[name] = struct{}{}
			}
		}
	}
}

// RememberKubeContext adds a context the helm poller watches.
func (s *Service) RememberKubeContext(name string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	s.watchedContextsMu.Lock()
	defer s.watchedContextsMu.Unlock()
	if s.watchedContexts == nil {
		s.watchedContexts = make(map[string]struct{})
	}
	s.watchedContexts[name] = struct{}{}
}

func (s *Service) snapshotConfiguredKubeContexts() []string {
	s.watchedContextsMu.Lock()
	defer s.watchedContextsMu.Unlock()
	out := make([]string, 0, len(s.watchedContexts))
	for name := range s.watchedContexts {
		out = append(out, name)
	}
	return out
}

// watchedKubeContexts limits the helm poller to contexts in use.
func (s *Service) watchedKubeContexts() []string {
	seen := make(map[string]struct{})
	var contexts []string
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		contexts = append(contexts, name)
	}
	for _, name := range s.snapshotConfiguredKubeContexts() {
		add(name)
	}
	for _, entry := range s.queue.list() {
		if entry.Status == StatusRunning {
			add(entry.KubernetesContext)
		}
	}
	for _, selection := range s.d.Sessions.OpenSelections() {
		add(selection.KubernetesContext)
	}
	return contexts
}
