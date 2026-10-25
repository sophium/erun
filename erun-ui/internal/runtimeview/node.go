package runtimeview

import (
	"strings"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// A stopped node and a missing or stale reading ("" or "unknown") must never
// collapse into each other; both reach the frontend verbatim.

// NodeSnapshot returns nil when no erun-managed node backs the env, which must
// render nothing rather than "stopped".
func (s *Service) NodeSnapshot(cloudProviderAlias, kubernetesContext string) *NodeSnapshot {
	context, ok, err := s.d.LinkedCloudContextFor(cloudProviderAlias, kubernetesContext)
	if err != nil || !ok {
		return nil
	}
	name := strings.TrimSpace(context.Name)
	if name == "" {
		return nil
	}
	return &NodeSnapshot{
		Name:   name,
		Label:  cloudContextDisplayName(context),
		Status: strings.TrimSpace(context.Status),
	}
}

func cloudContextDisplayName(status model.CloudContextStatus) string {
	if name := strings.TrimSpace(status.KubernetesContext); name != "" {
		return name
	}
	return strings.TrimSpace(status.Name)
}

// RefreshNodeStatuses republishes changed node readings; it reads only local
// state, never the cloud.
func (s *Service) RefreshNodeStatuses() {
	tenants, err := s.d.Store.ListTenantConfigs()
	if err != nil {
		return
	}
	for _, tenant := range tenants {
		envs, err := s.d.Store.ListEnvConfigs(tenant.Name)
		if err != nil {
			continue
		}
		for _, env := range envs {
			selection := uimodel.Selection{
				Tenant:      strings.TrimSpace(tenant.Name),
				Environment: strings.TrimSpace(env.Name),
			}
			s.emitEnvNodeIfChanged(selection, s.NodeSnapshot(env.CloudProviderAlias, env.KubernetesContext))
		}
	}
}

func (s *Service) emitEnvNodeIfChanged(selection uimodel.Selection, node *NodeSnapshot) {
	key := uimodel.SelectionKey(selection)
	var current NodeSnapshot
	if node != nil {
		current = *node
	}
	s.nodesMu.Lock()
	previous, seen := s.nodes[key]
	unchanged := seen && previous == current
	if !unchanged {
		if s.nodes == nil {
			s.nodes = make(map[string]NodeSnapshot)
		}
		s.nodes[key] = current
	}
	s.nodesMu.Unlock()
	if unchanged {
		return
	}
	s.d.Emit(uimodel.EnvNodeEvent, NodePayload{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
		Node:        node,
	})
}
