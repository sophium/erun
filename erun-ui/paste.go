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

// pastedFileDir reuses the generic Codex attachments dir; it predates this
// feature accepting non-image files.
const pastedFileDir = "/home/erun/.codex/attachments"

// maxPastedFileBytes caps a paste because the copy buffers the entire base64
// payload in memory; an unbounded paste could exhaust it.
const maxPastedFileBytes = 100 * 1024 * 1024

type pastedFileSaveParams struct {
	Result   eruncommon.OpenResult
	Data     []byte
	MIMEType string
	Name     string
}

func savePastedFileToRuntime(params pastedFileSaveParams) (string, error) {
	if len(params.Data) == 0 {
		return "", fmt.Errorf("pasted file data is empty")
	}
	if len(params.Data) > maxPastedFileBytes {
		return "", fmt.Errorf("pasted file is too large (%d bytes); the limit is %d bytes", len(params.Data), maxPastedFileBytes)
	}

	remoteDir := pastedFileRemoteDir()
	remotePath := path.Join(remoteDir, pastedFileFilename(time.Now().UTC(), params.MIMEType, params.Name))
	name, args, _ := buildPastedFileCopyCommand(params.Result, remoteDir, remotePath)

	cmd := exec.Command(name, args...)
	cmd.Stdin = strings.NewReader(base64.StdEncoding.EncodeToString(params.Data))
	output, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if detail == "" {
			return "", fmt.Errorf("copy pasted file into runtime: %w", err)
		}
		return "", fmt.Errorf("copy pasted file into runtime: %w: %s", err, detail)
	}
	return remotePath, nil
}

func buildPastedFileCopyCommand(result eruncommon.OpenResult, remoteDir, remotePath string) (string, []string, string) {
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
		eruncommon.DevopsComponentName,
		"deployment/"+release,
		"--",
		"/bin/sh",
		"-lc",
		script,
	)
	return "kubectl", args, script
}

func pastedFileRemoteDir() string {
	return pastedFileDir
}

// pastedFileFilename preserves the sanitized clipboard name so an agent sees the
// real name, not paste-….bin; the timestamp prefix keeps names collision-proof.
func pastedFileFilename(now time.Time, mimeType, name string) string {
	stamp := now.Format("20060102-150405.000000000")
	if sanitized := sanitizePastedFileName(name); sanitized != "" {
		return "paste-" + stamp + "-" + sanitized
	}
	return "paste-" + stamp + pastedFileExtension(mimeType)
}

// sanitizePastedFileName reduces a clipboard name to one path segment so
// path.Join with it cannot escape the staging dir.
func sanitizePastedFileName(name string) string {
	name = strings.TrimSpace(name)
	if i := strings.LastIndexAny(name, "/\\"); i >= 0 {
		name = name[i+1:]
	}
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '/' || r == '\\' {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return ""
	}
	return name
}

func pastedFileExtension(mimeType string) string {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	}
	return ".bin"
}
