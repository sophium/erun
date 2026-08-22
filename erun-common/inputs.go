package eruncommon

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"
)

// UploadRuntimeInputParams selects the file `inputs upload` places inside the
// runtime pod.
type UploadRuntimeInputParams struct {
	// LocalPath is the host filesystem source; must be a regular file.
	LocalPath string
	// RemotePath is the absolute destination path inside the pod, including the
	// file name. It is never defaulted: the caller must always name exactly
	// where the file lands, so it can never land inside a directory the
	// workspace-sync mirror or another background process reconciles away.
	RemotePath string
}

// UploadRuntimeInputResult is the result of `inputs upload`.
type UploadRuntimeInputResult struct {
	RemotePath string `json:"remotePath"`
	Bytes      int64  `json:"bytes"`
	SHA256     string `json:"sha256"`
}

// RuntimeInputUploadRunner runs a /bin/sh script in the env's runtime pod,
// streaming stdin to it, and returns its captured output.
type RuntimeInputUploadRunner func(req ShellLaunchParams, script string, stdin io.Reader) (RemoteCommandResult, error)

// UploadRuntimeInput streams one local file into the env's runtime pod over
// the same kubectl-exec transport ExecShell uses to seed an SSH key onto
// stdin, so the bytes never pass through the model's context and never touch
// a command line. Dry-run returns a preview result with no bytes sent.
//
// This is a CLI-only capability. An in-pod MCP tool cannot read a path on the
// operator's host, and for a remote-agent env there is no host filesystem
// path into the pod at all — so the host CLI is the only side that can
// originate this transfer. See root AGENTS.md § "Command primitives vs
// orchestration" for the both-transports default this deviates from.
func UploadRuntimeInput(ctx Context, req ShellLaunchParams, params UploadRuntimeInputParams, run RuntimeInputUploadRunner) (UploadRuntimeInputResult, error) {
	localPath := strings.TrimSpace(params.LocalPath)
	if localPath == "" {
		return UploadRuntimeInputResult{}, fmt.Errorf("local path is required")
	}
	remotePath := strings.TrimSpace(params.RemotePath)
	if remotePath == "" {
		return UploadRuntimeInputResult{}, fmt.Errorf("remote path is required")
	}
	dir, name, err := splitRuntimeInputDestination(remotePath)
	if err != nil {
		return UploadRuntimeInputResult{}, err
	}
	info, err := os.Stat(localPath)
	if err != nil {
		return UploadRuntimeInputResult{}, fmt.Errorf("local file %q: %w", localPath, err)
	}
	if info.IsDir() {
		return UploadRuntimeInputResult{}, fmt.Errorf("local path %q is a directory; inputs upload transfers one file at a time", localPath)
	}
	if run == nil {
		run = RunRemoteCommandWithStdin
	}
	target := path.Join(dir, name)
	ctx.Trace(fmt.Sprintf("inputs: uploading %s (%d bytes) to %s", localPath, info.Size(), target))
	script := runtimeInputUploadScript(dir, name)
	if traced := traceRuntimeInputUploadScript(ctx, req, "inputs upload script", script); traced {
		return UploadRuntimeInputResult{RemotePath: target, Bytes: info.Size()}, nil
	}
	f, err := os.Open(localPath)
	if err != nil {
		return UploadRuntimeInputResult{}, fmt.Errorf("local file %q: %w", localPath, err)
	}
	defer func() { _ = f.Close() }()
	hasher := sha256.New()
	out, err := run(req, script, io.TeeReader(f, hasher))
	if err != nil {
		return UploadRuntimeInputResult{}, fmt.Errorf("upload runtime input %q%s: %w", target, formatRemoteCommandStderr(out.Stderr), err)
	}
	remoteSize, remoteSHA, err := parseRuntimeInputUploadResponse(out.Stdout)
	if err != nil {
		return UploadRuntimeInputResult{}, fmt.Errorf("upload runtime input %q: %w", target, err)
	}
	localSHA := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(remoteSHA, localSHA) {
		return UploadRuntimeInputResult{}, fmt.Errorf("upload runtime input %q: checksum mismatch after transfer (local %s, remote %s)", target, localSHA, remoteSHA)
	}
	if remoteSize != info.Size() {
		return UploadRuntimeInputResult{}, fmt.Errorf("upload runtime input %q: size mismatch after transfer (local %d bytes, remote %d bytes)", target, info.Size(), remoteSize)
	}
	return UploadRuntimeInputResult{RemotePath: target, Bytes: info.Size(), SHA256: localSHA}, nil
}

// splitRuntimeInputDestination validates the remote path and splits it into
// the directory the script writes into and the sanitized entry name, reusing
// the same path validation outputs relies on for its own pod-side paths.
func splitRuntimeInputDestination(remotePath string) (dir, name string, err error) {
	cleaned, err := validateAbsolutePodPath(remotePath, "remote path")
	if err != nil {
		return "", "", err
	}
	if cleaned == "/" {
		return "", "", fmt.Errorf("remote path must include a file name: %q", remotePath)
	}
	name, err = sanitizeOutputEntryName(path.Base(cleaned))
	if err != nil {
		return "", "", err
	}
	return path.Dir(cleaned), name, nil
}

// traceRuntimeInputUploadScript returns true when the context is in dry-run,
// in which case the caller must not run the command.
func traceRuntimeInputUploadScript(ctx Context, req ShellLaunchParams, label, script string) bool {
	traceArgs := append([]string{}, kubectlRemoteExecStdinArgs(req, script)...)
	if len(traceArgs) > 0 {
		traceArgs[len(traceArgs)-1] = "<remote-script>"
	}
	ctx.TraceCommand("", "kubectl", traceArgs...)
	ctx.TraceBlock(label, script)
	return ctx.DryRun
}

// runtimeInputUploadScript writes stdin to dir/name atomically (via a
// same-directory temp file and rename, so a killed transfer never leaves a
// partial file visible at the final path) and reports the written size and
// sha256 so the caller can verify the transfer landed byte-identical.
func runtimeInputUploadScript(dir, name string) string {
	quotedDir := shellQuote(dir)
	quotedName := shellQuote(name)
	return strings.Join([]string{
		"set -e",
		"dir=" + quotedDir,
		"name=" + quotedName,
		"mkdir -p \"$dir\" 2>/dev/null || true",
		"if [ ! -d \"$dir\" ] || [ ! -w \"$dir\" ]; then echo \"erun-inputs: destination directory is not writable: $dir\" >&2; exit 3; fi",
		"tmp=\"$dir/.$name.erun-upload-tmp\"",
		"cat > \"$tmp\"",
		"mv \"$tmp\" \"$dir/$name\"",
		"size=$(stat -c %s \"$dir/$name\" 2>/dev/null || echo 0)",
		"sha=$(sha256sum \"$dir/$name\" 2>/dev/null | awk '{print $1}')",
		"printf '%s\\t%s\\n' \"$size\" \"$sha\"",
	}, "\n")
}

// parseRuntimeInputUploadResponse decodes the upload script's response: a
// single "<size>\t<sha256>" line.
func parseRuntimeInputUploadResponse(stdout string) (size int64, sha string, err error) {
	trimmed := strings.TrimSpace(stdout)
	if trimmed == "" {
		return 0, "", fmt.Errorf("empty response")
	}
	sizeField, shaField, ok := strings.Cut(trimmed, "\t")
	if !ok {
		return 0, "", fmt.Errorf("malformed response: %q", stdout)
	}
	size, err = strconv.ParseInt(strings.TrimSpace(sizeField), 10, 64)
	if err != nil {
		return 0, "", fmt.Errorf("malformed size in response: %q", stdout)
	}
	sha = strings.TrimSpace(shaField)
	if sha == "" {
		return 0, "", fmt.Errorf("missing checksum in response: %q", stdout)
	}
	return size, sha, nil
}

// kubectlRemoteExecStdinArgs mirrors kubectlRemoteExecArgs but adds -i so
// stdin is attached: the local file's bytes stream through it, never through
// argv, matching the pattern seedRemoteSSHKey already uses to seed a key.
func kubectlRemoteExecStdinArgs(req ShellLaunchParams, script string) []string {
	args := kubectlTargetArgs(req)
	args = append(args, "exec", "-i", "deployment/"+RuntimeReleaseName(req.Tenant), "--", "/bin/sh", "-c", script)
	return args
}

// RunRemoteCommandWithStdin is the stdin-streaming counterpart of
// RunRemoteCommand.
func RunRemoteCommandWithStdin(req ShellLaunchParams, script string, stdin io.Reader) (RemoteCommandResult, error) {
	cmd := Command("kubectl", kubectlRemoteExecStdinArgs(req, script)...)
	cmd.Stdin = stdin
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return RemoteCommandResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}, err
}
