package eruncommon

import "testing"

// TestResolveContainerRegistryPrecedence pins the order resolveContainerRegistry
// tries each source in, since that order is the thing most likely to regress
// silently: an explicit flag must beat project config, project config must beat
// a previously stored value, and only a brand-new environment falls through to
// the default/prompt step.
func TestResolveContainerRegistryPrecedence(t *testing.T) {
	tests := []struct {
		name             string
		params           BootstrapInitParams
		projectRoot      string
		projectRegistry  string
		current          string
		creating         bool
		autoApprove      bool
		promptRegistry   string
		wantRegistry     string
		wantPromptCalled bool
	}{
		{
			name:         "cluster registry short-circuits everything else",
			params:       BootstrapInitParams{ClusterRegistry: &ClusterRegistry{}, ContainerRegistry: "explicit"},
			current:      "stored",
			creating:     true,
			wantRegistry: "",
		},
		{
			name:            "explicit container registry beats project config and current",
			params:          BootstrapInitParams{ContainerRegistry: "explicit"},
			projectRoot:     "/project",
			projectRegistry: "project",
			current:         "stored",
			wantRegistry:    "explicit",
		},
		{
			name:            "project config beats a previously stored current",
			projectRoot:     "/project",
			projectRegistry: "project",
			current:         "stored",
			wantRegistry:    "project",
		},
		{
			name:         "a previously stored current wins when nothing else resolves",
			current:      "stored",
			wantRegistry: "stored",
		},
		{
			name:         "an existing environment with nothing resolved stays empty",
			creating:     false,
			wantRegistry: "",
		},
		{
			name:         "a new environment with auto-approve falls back to the default",
			creating:     true,
			autoApprove:  true,
			wantRegistry: DefaultContainerRegistry,
		},
		{
			name:             "a new environment without auto-approve prompts",
			creating:         true,
			promptRegistry:   "prompted",
			wantRegistry:     "prompted",
			wantPromptCalled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			promptCalled := false
			runner := bootstrapRunner{BootstrapInitDependencies: BootstrapInitDependencies{
				LoadProjectConfig: func(string) (ProjectConfig, string, error) {
					cfg := ProjectConfig{}
					if tt.projectRegistry != "" {
						cfg.SetContainerRegistriesForEnvironment("dev", SingleContainerRegistries(tt.projectRegistry))
					}
					return cfg, "", nil
				},
				PromptContainerRegistry: func(string) (string, error) {
					promptCalled = true
					return tt.promptRegistry, nil
				},
			}}

			tt.params.AutoApprove = tt.autoApprove
			registry, err := runner.resolveContainerRegistry(tt.params, "acme", "dev", tt.projectRoot, tt.current, tt.creating)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if registry != tt.wantRegistry {
				t.Fatalf("registry = %q, want %q", registry, tt.wantRegistry)
			}
			if promptCalled != tt.wantPromptCalled {
				t.Fatalf("prompt called = %v, want %v", promptCalled, tt.wantPromptCalled)
			}
		})
	}
}
