package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/sophium/erun/erun-common/base/seam"
	settingssvc "github.com/sophium/erun/erun-ui/internal/settings"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// The configuration surface; the behavior lives in internal/settings. The two
// directory pickers stay here because they open a native dialog.

func (a *App) CheckHostedDefinitionDrift(tenant, environment string) (settingssvc.HostedDefinitionDrift, error) {
	return a.settings.CheckHostedDefinitionDrift(tenant, environment)
}

func (a *App) ClearGatewayCredential() (settingssvc.GatewayCredentialStatus, error) {
	return a.settings.ClearGatewayCredential()
}

func (a *App) DescribeCloudContextApiStop(name string) (settingssvc.CloudContextStatus, error) {
	return a.settings.DescribeCloudContextApiStop(name)
}

func (a *App) DisableCloudContextApiStop(name string) (settingssvc.CloudContextStatus, error) {
	return a.settings.DisableCloudContextApiStop(name)
}

func (a *App) EnableCloudContextApiStop(name string) (settingssvc.CloudContextStatus, error) {
	return a.settings.EnableCloudContextApiStop(name)
}

func (a *App) GetCloudProviderBearerToken(alias string) (settingssvc.CloudProviderBearerToken, error) {
	return a.settings.GetCloudProviderBearerToken(alias)
}

func (a *App) InitAWSCloudProvider(input settingssvc.AWSCloudAliasInput) (settingssvc.CloudProviderStatus, error) {
	return a.settings.InitAWSCloudProvider(input)
}

func (a *App) InitCloudContext(input settingssvc.CloudContextInitInput) (settingssvc.CloudContextStatus, error) {
	return a.settings.InitCloudContext(input)
}

func (a *App) LoadCloudContextStatuses() ([]settingssvc.CloudContextStatus, error) {
	return a.settings.LoadCloudContextStatuses()
}

func (a *App) LoadCloudProviderStatuses() ([]settingssvc.CloudProviderStatus, error) {
	return a.settings.LoadCloudProviderStatuses()
}

func (a *App) LoadERunConfig() (settingssvc.ERunConfig, error) {
	return a.settings.LoadERunConfig()
}

func (a *App) LoadEnvironmentConfig(selection uimodel.Selection) (settingssvc.EnvironmentConfig, error) {
	return a.settings.LoadEnvironmentConfig(selection)
}

func (a *App) LoadEnvironmentGatewayRouting(selection uimodel.Selection) (settingssvc.GatewayRouting, error) {
	return a.settings.LoadEnvironmentGatewayRouting(selection)
}

func (a *App) LoadGatewayCredentialStatus() (settingssvc.GatewayCredentialStatus, error) {
	return a.settings.LoadGatewayCredentialStatus()
}

func (a *App) LoadGatewayModels(baseURL string) ([]settingssvc.GatewayModel, error) {
	return a.settings.LoadGatewayModels(baseURL)
}

func (a *App) LoadGatewayRoutingDrift() ([]settingssvc.GatewayRouting, error) {
	return a.settings.LoadGatewayRoutingDrift()
}

func (a *App) LoadHostGatewayDefaults() (settingssvc.HostGatewayDefaults, error) {
	return a.settings.LoadHostGatewayDefaults()
}

func (a *App) LoadTenantConfig(tenant string) (settingssvc.TenantConfig, error) {
	return a.settings.LoadTenantConfig(tenant)
}

func (a *App) LoginCloudProvider(alias string) (settingssvc.CloudProviderStatus, error) {
	return a.settings.LoginCloudProvider(alias)
}

func (a *App) LogoutCloudProvider(alias string) (settingssvc.CloudProviderStatus, error) {
	return a.settings.LogoutCloudProvider(alias)
}

func (a *App) SaveAWSCloudProviderAlias(input settingssvc.AWSCloudAliasInput) (settingssvc.CloudProviderStatus, error) {
	return a.settings.SaveAWSCloudProviderAlias(input)
}

func (a *App) SaveERunConfig(config settingssvc.ERunConfig) (settingssvc.ERunConfig, error) {
	return a.settings.SaveERunConfig(config)
}

func (a *App) SaveEnvironmentConfig(selection uimodel.Selection, config settingssvc.EnvironmentConfig) (settingssvc.EnvironmentConfig, error) {
	return a.settings.SaveEnvironmentConfig(selection, config)
}

func (a *App) SaveGatewayCredential(token string) (settingssvc.GatewayCredentialStatus, error) {
	return a.settings.SaveGatewayCredential(token)
}

func (a *App) SaveTenantConfig(config settingssvc.TenantConfig) (settingssvc.TenantConfig, error) {
	return a.settings.SaveTenantConfig(config)
}

func (a *App) SetEnvironmentAutoStart(selection uimodel.Selection, mode string) (settingssvc.EnvironmentConfig, error) {
	return a.settings.SetEnvironmentAutoStart(selection, mode)
}

func (a *App) SetupCloudProviderOIDC(alias string) (settingssvc.CloudProviderStatus, error) {
	return a.settings.SetupCloudProviderOIDC(alias)
}

func (a *App) StartCloudContext(name string) (settingssvc.CloudContextStatus, error) {
	return a.settings.StartCloudContext(name)
}

func (a *App) StopCloudContext(name string) (settingssvc.CloudContextStatus, error) {
	return a.settings.StopCloudContext(name)
}

func (a *App) SwitchCloudProviderIdentity(alias string) (settingssvc.CloudProviderStatus, error) {
	return a.settings.SwitchCloudProviderIdentity(alias)
}

func (a *App) UploadHostedDefinition(tenant, environment string) (settingssvc.HostedDefinitionUpload, error) {
	return a.settings.UploadHostedDefinition(tenant, environment)
}

func (a *App) ChooseWorkspaceSyncLocalFolder(selection uimodel.Selection, current string) (string, error) {
	selection = uimodel.NormalizeSelection(selection)
	if err := uimodel.ErrMissingTenantOrEnvironment("choose workspace-sync local folder", selection.Tenant, selection.Environment); err != nil {
		return "", err
	}
	if a.ctx == nil {
		return "", fmt.Errorf("application context is not ready")
	}
	defaultDirectory := strings.TrimSpace(current)
	if defaultDirectory == "" {
		defaultDirectory = resolveWorkspaceSyncDialogDefaultDirectory(a.deps.findProjectRoot)
	}
	if defaultDirectory != "" {
		if info, err := os.Stat(defaultDirectory); err != nil || !info.IsDir() {
			defaultDirectory = ""
		}
	}
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title:            fmt.Sprintf("Select local sync folder for %s/%s", selection.Tenant, selection.Environment),
		DefaultDirectory: defaultDirectory,
	})
}

func resolveWorkspaceSyncDialogDefaultDirectory(findProjectRoot seam.ProjectFinderFunc) string {
	if findProjectRoot == nil {
		return ""
	}
	_, projectRoot, err := findProjectRoot()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(projectRoot)
}

// ChooseLocalRepoPath opens a native directory picker for the local repo path.
func (a *App) ChooseLocalRepoPath(current string) (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("application context is not ready")
	}
	defaultDirectory := strings.TrimSpace(current)
	if defaultDirectory != "" {
		if info, err := os.Stat(defaultDirectory); err != nil || !info.IsDir() {
			defaultDirectory = ""
		}
	}
	if defaultDirectory == "" {
		defaultDirectory = resolveWorkspaceSyncDialogDefaultDirectory(a.deps.findProjectRoot)
	}
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title:            "Select local repo path",
		DefaultDirectory: defaultDirectory,
	})
}
