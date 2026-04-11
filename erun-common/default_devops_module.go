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
	projectRoot = strings.TrimSpace(projectRoot)
	tenant = strings.TrimSpace(tenant)
	if projectRoot == "" || tenant == "" {
		return nil
	}
	projectRoot = filepath.Clean(projectRoot)

	moduleName := RuntimeReleaseName(tenant)
	replacer := strings.NewReplacer("__MODULE_NAME__", moduleName)
	for _, templateFile := range defaultDevopsModuleTemplates {
		data, err := defaultDevopsModuleFiles.ReadFile(templateFile.AssetPath)
		if err != nil {
			return err
		}

		targetPath := replacer.Replace(templateFile.TargetPath)
		resolvedPath := filepath.Join(projectRoot, filepath.FromSlash(targetPath))
		content := []byte(replacer.Replace(string(data)))
		if err := ensureDefaultDevopsModuleFile(ctx, resolvedPath, templateFile.Mode, content); err != nil {
			return err
		}
	}

	return nil
}

func ensureDefaultDevopsModuleFile(ctx Context, path string, mode os.FileMode, content []byte) error {
	info, err := os.Stat(path)
	switch {
	case err == nil && info.IsDir():
		return fmt.Errorf("%q is a directory", path)
	case err == nil:
		existing, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Equal(existing, content) {
			return nil
		}
		if !shouldReplaceDefaultDevopsModuleFile(path, existing) {
			return nil
		}
	case !os.IsNotExist(err):
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

func shouldReplaceDefaultDevopsModuleFile(path string, existing []byte) bool {
	if filepath.Base(path) != "Dockerfile" {
		return false
	}

	legacy := []string{
		"ARG ERUN_BASE_IMAGE=erunpaas/erun-devops\nARG ERUN_BASE_VERSION=1.0.0\n\nFROM ${ERUN_BASE_IMAGE}:${ERUN_BASE_VERSION}\n",
		"ARG ERUN_BASE_TAG=erunpaas/erun-devops:1.0.0\n\nFROM ${ERUN_BASE_TAG}\n",
	}
	current := strings.TrimSpace(string(existing))
	for _, candidate := range legacy {
		if current == strings.TrimSpace(candidate) {
			return true
		}
	}
	return false
}
