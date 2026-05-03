package eruncommon

import (
	"bytes"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed assets/default-devops-module/VERSION assets/default-devops-module/docker/component/Dockerfile
var defaultDevopsModuleFiles embed.FS

type defaultDevopsModuleTemplate struct {
	AssetPath  string
	TargetPath string
	Mode       os.FileMode
}

var defaultDevopsModuleTemplates = []defaultDevopsModuleTemplate{
	{
		AssetPath:  "assets/default-devops-module/VERSION",
		TargetPath: "__MODULE_NAME__/VERSION",
		Mode:       0o644,
	},
	{
		AssetPath:  "assets/default-devops-module/docker/component/Dockerfile",
		TargetPath: "__MODULE_NAME__/docker/__MODULE_NAME__/Dockerfile",
		Mode:       0o644,
	},
}

func EnsureDefaultDevopsModule(ctx Context, projectRoot, tenant string) error {
	return EnsureDefaultDevopsModuleWithVersion(ctx, projectRoot, tenant, "")
}

func EnsureDefaultDevopsModuleWithVersion(ctx Context, projectRoot, tenant, runtimeVersion string) error {
	projectRoot = strings.TrimSpace(projectRoot)
	tenant = strings.TrimSpace(tenant)
	if projectRoot == "" || tenant == "" {
		return nil
	}
	projectRoot = filepath.Clean(projectRoot)

	moduleName := RuntimeReleaseName(tenant)
	for _, templateFile := range defaultDevopsModuleTemplates {
		data, err := defaultDevopsModuleFiles.ReadFile(templateFile.AssetPath)
		if err != nil {
			return err
		}

		targetPath := strings.ReplaceAll(templateFile.TargetPath, "__MODULE_NAME__", moduleName)
		resolvedPath := filepath.Join(projectRoot, filepath.FromSlash(targetPath))
		content := renderDefaultDevopsModuleTemplate(templateFile.AssetPath, moduleName, runtimeVersion, data)
		if err := ensureDefaultDevopsFile(ctx, resolvedPath, templateFile.Mode, content); err != nil {
			return err
		}
	}

	return nil
}

func renderDefaultDevopsModuleTemplate(assetPath, moduleName, runtimeVersion string, data []byte) []byte {
	content := strings.ReplaceAll(string(data), "__MODULE_NAME__", moduleName)

	switch assetPath {
	case "assets/default-devops-module/docker/component/Dockerfile":
		content = strings.Replace(content, "ARG ERUN_BASE_TAG=erunpaas/erun-devops:1.0.0", "ARG ERUN_BASE_TAG="+defaultDevopsBaseTag(runtimeVersion), 1)
	}

	return []byte(content)
}

func defaultDevopsBaseTag(runtimeVersion string) string {
	runtimeVersion = strings.TrimSpace(runtimeVersion)
	if runtimeVersion == "" {
		runtimeVersion = "dev"
	}
	return "erunpaas/erun-devops:" + runtimeVersion
}

func ensureDefaultDevopsFile(ctx Context, path string, mode os.FileMode, content []byte) error {
	replace, err := shouldWriteDefaultDevopsFile(path, content)
	if err != nil || !replace {
		return err
	}

	ctx.TraceCommand("", "mkdir", "-p", filepath.Dir(path))
	ctx.TraceCommand("", "write-file", path)
	if ctx.DryRun {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, content, mode); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}

func shouldWriteDefaultDevopsFile(path string, content []byte) (bool, error) {
	info, err := os.Stat(path)
	switch {
	case err == nil && info.IsDir():
		return false, fmt.Errorf("%q is a directory", path)
	case err == nil:
		return shouldWriteExistingDefaultDevopsFile(path, content)
	case os.IsNotExist(err):
		return true, nil
	default:
		return false, err
	}
}

func shouldWriteExistingDefaultDevopsFile(path string, content []byte) (bool, error) {
	existing, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	if bytes.Equal(existing, content) {
		return false, nil
	}
	return shouldReplaceDefaultDevopsFile(path, existing, content), nil
}

func shouldReplaceDefaultDevopsFile(path string, existing, content []byte) bool {
	current := strings.TrimSpace(string(existing))
	for _, candidate := range defaultDevopsLegacyContents(path, content) {
		if current == strings.TrimSpace(candidate) {
			return true
		}
	}
	return false
}

func defaultDevopsLegacyContents(path string, content []byte) []string {
	switch filepath.Base(path) {
	case "Dockerfile":
		return []string{
			"ARG ERUN_BASE_IMAGE=erunpaas/erun-devops\nARG ERUN_BASE_VERSION=1.0.0\n\nFROM ${ERUN_BASE_IMAGE}:${ERUN_BASE_VERSION}\n",
			"ARG ERUN_BASE_TAG=erunpaas/erun-devops:1.0.0\n\nFROM ${ERUN_BASE_TAG}\n",
			"ARG ERUN_BASE_TAG=erunpaas/erun-devops:1.0.0\n\nFROM ${ERUN_BASE_TAG}\n\nENTRYPOINT [\"/bin/sh\", \"-lc\", \"if [ \\\"${1:-}\\\" = shell ]; then shift; repo_dir=\\\"${ERUN_REPO_PATH:-${HOME}/git/erun}\\\"; [ -d \\\"$repo_dir\\\" ] && cd \\\"$repo_dir\\\"; exec /bin/bash -i; fi; exec erun-devops-entrypoint \\\"$@\\\"\", \"erun-devops-wrapper\"]\n",
		}
	case "service.yaml":
		return []string{legacyDefaultDevopsServiceTemplate(content)}
	}
	return nil
}

func legacyDefaultDevopsServiceTemplate(content []byte) string {
	return strings.NewReplacer(
		"{{- $mcpPort := default 17000 .Values.mcpPort -}}\n{{- $apiPort := default 17033 .Values.apiPort -}}\n{{- $sshPort := default 17022 .Values.sshPort -}}\n",
		"",
		"{{- $api := default dict .Values.api -}}\n{{- $oidcAllowedIssuers := default \"\" $api.oidcAllowedIssuers -}}\n",
		"",
		"{{- $cloudContext := default dict .Values.cloudContext -}}\n{{- $cloudContextName := default \"\" $cloudContext.name -}}\n{{- $cloudProviderAlias := default \"\" $cloudContext.providerAlias -}}\n{{- $cloudRegion := default \"\" $cloudContext.region -}}\n{{- $cloudInstanceID := default \"\" $cloudContext.instanceId -}}\n",
		"",
		"            - name: ERUN_CLOUD_CONTEXT_NAME\n              value: {{ $cloudContextName | quote }}\n            - name: ERUN_CLOUD_PROVIDER_ALIAS\n              value: {{ $cloudProviderAlias | quote }}\n            - name: ERUN_CLOUD_REGION\n              value: {{ $cloudRegion | quote }}\n            - name: ERUN_CLOUD_INSTANCE_ID\n              value: {{ $cloudInstanceID | quote }}\n",
		"",
		"            - name: ERUN_MCP_PORT\n              value: {{ $mcpPort | quote }}\n            - name: ERUN_API_PORT\n              value: {{ $apiPort | quote }}\n            - name: ERUN_OIDC_ALLOWED_ISSUERS\n              value: {{ $oidcAllowedIssuers | quote }}\n            - name: ERUN_SSHD_PORT\n              value: {{ $sshPort | quote }}\n",
		"",
		"            - containerPort: {{ $mcpPort }}\n              name: mcp\n            - containerPort: {{ $apiPort }}\n              name: api\n            - containerPort: {{ $sshPort }}\n              name: ssh",
		"            - containerPort: 17000\n              name: mcp\n            - containerPort: 2222\n              name: ssh",
	).Replace(string(content))
}
