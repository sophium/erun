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
		content = strings.Replace(content, "ARG ERUN_BASE_TAG=ghcr.io/sophium/erun-devops:1.0.0", "ARG ERUN_BASE_TAG="+defaultDevopsBaseTag(runtimeVersion), 1)
	}

	return []byte(content)
}

func defaultDevopsBaseTag(runtimeVersion string) string {
	runtimeVersion = strings.TrimSpace(runtimeVersion)
	if runtimeVersion == "" {
		runtimeVersion = "dev"
	}
	return "ghcr.io/sophium/erun-devops:" + runtimeVersion
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
		"{{- $cloudContext := default dict .Values.cloudContext -}}\n{{- $cloudContextName := default \"\" $cloudContext.name -}}\n{{- $cloudProvider := default \"\" $cloudContext.provider -}}\n{{- $cloudProviderAlias := default \"\" $cloudContext.providerAlias -}}\n{{- $cloudRegion := default \"\" $cloudContext.region -}}\n{{- $cloudInstanceID := default \"\" $cloudContext.instanceId -}}\n",
		"",
		"{{- $claude := default dict .Values.claude -}}\n{{- $claudeUseBedrock := default \"1\" $claude.useBedrock -}}\n{{- $claudeUseMantle := default \"1\" $claude.useMantle -}}\n{{- $claudeSmallFastRegion := default $cloudRegion $claude.smallFastModelAWSRegion -}}\n{{- $claudeAvailableModels := default \"sonnet,haiku\" $claude.availableModels -}}\n{{- $claudeModel := default \"\" $claude.model -}}\n{{- $claudeDefaultOpusModel := default \"\" $claude.defaultOpusModel -}}\n{{- $claudeDefaultSonnetModel := default \"\" $claude.defaultSonnetModel -}}\n{{- $claudeDefaultHaikuModel := default \"\" $claude.defaultHaikuModel -}}\n{{- $claudeBedrockBaseURL := default \"\" $claude.bedrockBaseURL -}}\n{{- $claudeMantleBaseURL := default \"\" $claude.mantleBaseURL -}}\n{{- $claudeBedrockServiceTier := default \"\" $claude.bedrockServiceTier -}}\n{{- $claudeSkipMantleAuth := default \"\" $claude.skipMantleAuth -}}\n{{- $claudeDisablePromptCaching := default \"\" $claude.disablePromptCaching -}}\n{{- $claudeEnablePromptCaching1H := default \"\" $claude.enablePromptCaching1H -}}\n{{- $claudeMaxOutputTokens := default \"4096\" $claude.maxOutputTokens -}}\n{{- $claudeMaxThinkingTokens := default \"1024\" $claude.maxThinkingTokens -}}\n",
		"",
		"            - name: ERUN_CLOUD_CONTEXT_NAME\n              value: {{ $cloudContextName | quote }}\n            - name: ERUN_CLOUD_PROVIDER\n              value: {{ $cloudProvider | quote }}\n            - name: ERUN_CLOUD_PROVIDER_ALIAS\n              value: {{ $cloudProviderAlias | quote }}\n            - name: ERUN_CLOUD_REGION\n              value: {{ $cloudRegion | quote }}\n            - name: ERUN_CLOUD_INSTANCE_ID\n              value: {{ $cloudInstanceID | quote }}\n            {{ if eq $cloudProvider \"aws\" }}\n            - name: CLAUDE_CODE_USE_BEDROCK\n              value: {{ $claudeUseBedrock | quote }}\n            - name: CLAUDE_CODE_USE_MANTLE\n              value: {{ $claudeUseMantle | quote }}\n            - name: AWS_REGION\n              value: {{ $cloudRegion | quote }}\n            - name: ANTHROPIC_SMALL_FAST_MODEL_AWS_REGION\n              value: {{ $claudeSmallFastRegion | quote }}\n            - name: ERUN_CLAUDE_AVAILABLE_MODELS\n              value: {{ $claudeAvailableModels | quote }}\n            - name: CLAUDE_CODE_MAX_OUTPUT_TOKENS\n              value: {{ $claudeMaxOutputTokens | quote }}\n            - name: MAX_THINKING_TOKENS\n              value: {{ $claudeMaxThinkingTokens | quote }}\n            {{ if $claudeModel }}\n            - name: ANTHROPIC_MODEL\n              value: {{ $claudeModel | quote }}\n            {{ end }}\n            {{ if $claudeDefaultOpusModel }}\n            - name: ANTHROPIC_DEFAULT_OPUS_MODEL\n              value: {{ $claudeDefaultOpusModel | quote }}\n            {{ end }}\n            {{ if $claudeDefaultSonnetModel }}\n            - name: ANTHROPIC_DEFAULT_SONNET_MODEL\n              value: {{ $claudeDefaultSonnetModel | quote }}\n            {{ end }}\n            {{ if $claudeDefaultHaikuModel }}\n            - name: ANTHROPIC_DEFAULT_HAIKU_MODEL\n              value: {{ $claudeDefaultHaikuModel | quote }}\n            {{ end }}\n            {{ if $claudeBedrockBaseURL }}\n            - name: ANTHROPIC_BEDROCK_BASE_URL\n              value: {{ $claudeBedrockBaseURL | quote }}\n            {{ end }}\n            {{ if $claudeMantleBaseURL }}\n            - name: ANTHROPIC_BEDROCK_MANTLE_BASE_URL\n              value: {{ $claudeMantleBaseURL | quote }}\n            {{ end }}\n            {{ if $claudeBedrockServiceTier }}\n            - name: ANTHROPIC_BEDROCK_SERVICE_TIER\n              value: {{ $claudeBedrockServiceTier | quote }}\n            {{ end }}\n            {{ if $claudeSkipMantleAuth }}\n            - name: CLAUDE_CODE_SKIP_MANTLE_AUTH\n              value: {{ $claudeSkipMantleAuth | quote }}\n            {{ end }}\n            {{ if $claudeDisablePromptCaching }}\n            - name: DISABLE_PROMPT_CACHING\n              value: {{ $claudeDisablePromptCaching | quote }}\n            {{ end }}\n            {{ if $claudeEnablePromptCaching1H }}\n            - name: ENABLE_PROMPT_CACHING_1H\n              value: {{ $claudeEnablePromptCaching1H | quote }}\n            {{ end }}\n            {{ end }}\n",
		"",
		"            - name: ERUN_MCP_PORT\n              value: {{ $mcpPort | quote }}\n            - name: ERUN_API_PORT\n              value: {{ $apiPort | quote }}\n            - name: ERUN_OIDC_ALLOWED_ISSUERS\n              value: {{ $oidcAllowedIssuers | quote }}\n            - name: ERUN_SSHD_PORT\n              value: {{ $sshPort | quote }}\n",
		"",
		"            - containerPort: {{ $mcpPort }}\n              name: mcp\n            - containerPort: {{ $apiPort }}\n              name: api\n            - containerPort: {{ $sshPort }}\n              name: ssh",
		"            - containerPort: 17000\n              name: mcp\n            - containerPort: 2222\n              name: ssh",
	).Replace(string(content))
}
