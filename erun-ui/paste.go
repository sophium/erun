package main

import (
	"encoding/base64"
	"fmt"
	"os/exec"
	"path"
	"strings"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

const pastedImageDir = ".codex/attachments"

type pastedImageSaveParams struct {
	Result   eruncommon.OpenResult
	Data     []byte
	MIMEType string
	Name     string
}

func savePastedImageToRuntime(params pastedImageSaveParams) (string, error) {
	if len(params.Data) == 0 {
		return "", fmt.Errorf("pasted image data is empty")
	}

	extension, err := pastedImageExtension(params.MIMEType, params.Name)
	if err != nil {
		return "", err
	}

	remoteDir := pastedImageRemoteDir(params.Result)
	remotePath := path.Join(remoteDir, pastedImageFilename(time.Now().UTC(), extension))
	name, args, _ := buildPastedImageCopyCommand(params.Result, remoteDir, remotePath)

	cmd := exec.Command(name, args...)
	cmd.Stdin = strings.NewReader(base64.StdEncoding.EncodeToString(params.Data))
	output, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if detail == "" {
			return "", fmt.Errorf("copy pasted image into runtime: %w", err)
		}
		return "", fmt.Errorf("copy pasted image into runtime: %w: %s", err, detail)
	}
	return remotePath, nil
}

func buildPastedImageCopyCommand(result eruncommon.OpenResult, remoteDir, remotePath string) (string, []string, string) {
	shellParams := eruncommon.ShellLaunchParamsFromResult(result)
	release := eruncommon.RuntimeReleaseName(result.Tenant)
	script := fmt.Sprintf("mkdir -p %s && base64 -d > %s", shellQuote(remoteDir), shellQuote(remotePath))

	args := make([]string, 0, 12)
	if context := strings.TrimSpace(shellParams.KubernetesContext); context != "" {
		args = append(args, "--context", context)
	}
	if namespace := strings.TrimSpace(shellParams.Namespace); namespace != "" {
		args = append(args, "--namespace", namespace)
	}
	args = append(args,
		"exec",
		"-i",
		"-c",
		release,
		"deployment/"+release,
		"--",
		"/bin/sh",
		"-lc",
		script,
	)
	return "kubectl", args, script
}

func pastedImageRemoteDir(result eruncommon.OpenResult) string {
	shellParams := eruncommon.ShellLaunchParamsFromResult(result)
	return path.Join(eruncommon.RemoteShellWorktreePath(shellParams), pastedImageDir)
}

func pastedImageFilename(now time.Time, extension string) string {
	return "paste-" + now.Format("20060102-150405.000000000") + extension
}

func pastedImageExtension(mimeType, name string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "image/png":
		return ".png", nil
	case "image/jpeg", "image/jpg":
		return ".jpg", nil
	case "image/gif":
		return ".gif", nil
	case "image/webp":
		return ".webp", nil
	}

	name = strings.ToLower(strings.TrimSpace(name))
	switch {
	case strings.HasSuffix(name, ".png"):
		return ".png", nil
	case strings.HasSuffix(name, ".jpg"), strings.HasSuffix(name, ".jpeg"):
		return ".jpg", nil
	case strings.HasSuffix(name, ".gif"):
		return ".gif", nil
	case strings.HasSuffix(name, ".webp"):
		return ".webp", nil
	}

	return "", fmt.Errorf("unsupported pasted image type %q", mimeType)
}
