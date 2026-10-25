package settings

import (
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	configcloudcontext "github.com/sophium/erun/erun-common/base/config/cloudcontext"
	"github.com/sophium/erun/erun-common/base/model"
	cloudcloudcontext "github.com/sophium/erun/erun-common/infra/cloud/cloudcontext"
	"github.com/sophium/erun/erun-common/infra/cloud/providerconfig"
	"github.com/sophium/erun/erun-common/primitive/open/resolve"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func (s *Service) saveRemoteCloudAlias(selection uimodel.Selection, existing, updated configpkg.EnvConfig) error {
	if !existing.RemoteWorktree() || strings.TrimSpace(updated.CloudProviderAlias) == strings.TrimSpace(existing.CloudProviderAlias) {
		return nil
	}
	result, err := resolve.ResolveOpen(s.d.Store, model.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return err
	}
	ctx := s.d.Context()
	if err := s.d.EnsureMCPAvailable(ctx, result); err != nil {
		return err
	}
	bearer := s.d.MCPBearer(selection.Tenant, selection.Environment)
	_, err = s.d.SetRemoteCloudAlias(ctx, s.d.MCPEndpoint(result), bearer, selection.Tenant, selection.Environment, updated.CloudProviderAlias)
	return err
}

// environmentCloudProviderTypes is the render order for cloud-alias slots.
var environmentCloudProviderTypes = []string{
	configpkg.CloudProviderAWS,
	configpkg.CloudProviderCloudflare,
}

// environmentCloudAliasSlots omits types with no aliases and nothing attached,
// and keeps an attached alias whose provider config was deleted so it can be cleared.
func environmentCloudAliasSlots(store configpkg.CloudReadStore, config configpkg.EnvConfig) []EnvironmentCloudAlias {
	optionsByType := configuredCloudAliasesByType(store)
	attached := config.ResolvedCloudAliases()
	slots := make([]EnvironmentCloudAlias, 0, len(environmentCloudProviderTypes))
	for _, providerType := range environmentCloudProviderTypes {
		options := append([]string(nil), optionsByType[providerType]...)
		current := strings.TrimSpace(attached[providerType])
		if current != "" && !cloudAliasListContains(options, current) {
			options = append([]string{current}, options...)
		}
		if current == "" && len(options) == 0 {
			continue
		}
		slots = append(slots, EnvironmentCloudAlias{
			Provider: providerType,
			Alias:    current,
			Options:  options,
		})
	}
	return slots
}

func configuredCloudAliasesByType(store configpkg.CloudReadStore) map[string][]string {
	grouped := make(map[string][]string)
	providers, err := providerconfig.ListCloudProviders(store)
	if err != nil {
		return grouped
	}
	for _, provider := range providers {
		alias := strings.TrimSpace(provider.Alias)
		providerType := strings.ToLower(strings.TrimSpace(provider.Provider))
		if alias == "" || providerType == "" {
			continue
		}
		if cloudAliasListContains(grouped[providerType], alias) {
			continue
		}
		grouped[providerType] = append(grouped[providerType], alias)
	}
	return grouped
}

func cloudAliasListContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func environmentCloudProviderAliases(store configpkg.CloudReadStore, current string) []string {
	providers, err := providerconfig.ListCloudProviders(store)
	if err != nil {
		return nil
	}
	current = strings.TrimSpace(current)
	aliases := make([]string, 0, len(providers)+1)
	seen := make(map[string]struct{}, len(providers)+1)
	for _, provider := range providers {
		alias := strings.TrimSpace(provider.Alias)
		if alias == "" {
			continue
		}
		if _, ok := seen[alias]; ok {
			continue
		}
		aliases = append(aliases, alias)
		seen[alias] = struct{}{}
	}
	if current != "" {
		if _, ok := seen[current]; !ok {
			aliases = append([]string{current}, aliases...)
		}
	}
	return aliases
}

// applyEnvironmentCloudAliasSlots keeps AWS in the legacy CloudProviderAlias
// scalar; other types go in CloudProviderAliases.
func applyEnvironmentCloudAliasSlots(existing *configpkg.EnvConfig, config EnvironmentConfig) {
	if len(config.CloudAliasSlots) == 0 {
		existing.CloudProviderAlias = strings.TrimSpace(config.CloudProviderAlias)
		return
	}
	for _, slot := range config.CloudAliasSlots {
		providerType := strings.ToLower(strings.TrimSpace(slot.Provider))
		alias := strings.TrimSpace(slot.Alias)
		if providerType == "" || providerType == configpkg.CloudProviderAWS {
			existing.CloudProviderAlias = alias
			continue
		}
		if alias == "" {
			delete(existing.CloudProviderAliases, providerType)
			continue
		}
		if existing.CloudProviderAliases == nil {
			existing.CloudProviderAliases = make(map[string]string)
		}
		existing.CloudProviderAliases[providerType] = alias
	}
}

// LinkedCloudContext fills Status from the poller cache; "" means not yet
// observed, not stopped.
func (s *Service) LinkedCloudContext(config configpkg.EnvConfig) (model.CloudContextStatus, bool, error) {
	return s.LinkedCloudContextFor(config.CloudProviderAlias, config.KubernetesContext)
}

func (s *Service) LinkedCloudContextFor(cloudProviderAlias, kubernetesContext string) (model.CloudContextStatus, bool, error) {
	cloudProviderAlias = strings.TrimSpace(cloudProviderAlias)
	kubernetesContext = strings.TrimSpace(kubernetesContext)
	if kubernetesContext == "" {
		return model.CloudContextStatus{}, false, nil
	}
	statuses, err := cloudcloudcontext.ListCloudContextStatuses(s.d.Store)
	if err != nil {
		return model.CloudContextStatus{}, false, err
	}
	for _, status := range statuses {
		context := configcloudcontext.NormalizeCloudContextConfig(status.CloudContextConfig)
		if cloudProviderAlias != "" && strings.TrimSpace(context.CloudProviderAlias) != cloudProviderAlias {
			continue
		}
		if strings.TrimSpace(context.KubernetesContext) == kubernetesContext || strings.TrimSpace(context.Name) == kubernetesContext {
			status.CloudContextConfig = context
			status.Status = s.d.CloudStatuses.Status(context.Name)
			return status, true, nil
		}
	}
	return model.CloudContextStatus{}, false, nil
}
