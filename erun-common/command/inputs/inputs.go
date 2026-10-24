package inputs

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

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/kube"
)

// UploadRuntimeInputParams selects the file `inputs upload` places in the runtime pod.
type UploadRuntimeInputParams struct {
	LocalPath string
	// RemotePath is never defaulted, so a file cannot land somewhere a
	// background sync reconciles away.
	RemotePath string
}

// UploadRuntimeInputResult is the result of `inputs upload`.
type UploadRuntimeInputResult struct {
	RemotePath string `json:"remotePath"`
	Bytes      int64  `json:"bytes"`
	SHA256     string `json:"sha256"`
}

// RuntimeInputUploadRunner runs a /bin/sh script in the runtime pod with stdin streamed to it.
type RuntimeInputUploadRunner func(req config.ShellLaunchParams, script string, stdin io.Reader) (kube.RemoteCommandResult, error)

// UploadRuntimeInput streams a host file into the runtime pod over kubectl exec
// stdin, so the bytes never touch the model's context or a command line.
// It is host-only: the in-pod MCP edge has no path to the operator's filesystem.
func UploadRuntimeInput(ctx core.Context, req config.ShellLaunchParams, params UploadRuntimeInputParams, run RuntimeInputUploadRunner) (UploadRuntimeInputResult, error) {
	dir, name, info, err := resolveRuntimeInputUpload(params)
	if err != nil {
		return UploadRuntimeInputResult{}, err
	}
	if run == nil {
		run = RunRemoteCommandWithStdin
	}
	target := path.Join(dir, name)
	ctx.Trace(fmt.Sprintf("inputs: uploading %s (%d bytes) to %s", params.LocalPath, info.Size(), target))
	script := runtimeInputUploadScript(dir, name)
	if traced := traceRuntimeInputUploadScript(ctx, req, "inputs upload script", script); traced {
		return UploadRuntimeInputResult{RemotePath: target, Bytes: info.Size()}, nil
	}
	f, err := os.Open(params.LocalPath)
	if err != nil {
		return UploadRuntimeInputResult{}, fmt.Errorf("local file %q: %w", params.LocalPath, err)
	}
	defer func() { _ = f.Close() }()
	hasher := sha256.New()
	out, err := run(req, script, io.TeeReader(f, hasher))
	if err != nil {
		return UploadRuntimeInputResult{}, fmt.Errorf("upload runtime input %q%s: %w", target, kube.FormatRemoteCommandStderr(out.Stderr), err)
	}
	localSHA := hex.EncodeToString(hasher.Sum(nil))
	if err := verifyRuntimeInputUpload(target, localSHA, info.Size(), out.Stdout); err != nil {
		return UploadRuntimeInputResult{}, err
	}
	return UploadRuntimeInputResult{RemotePath: target, Bytes: info.Size(), SHA256: localSHA}, nil
}

func resolveRuntimeInputUpload(params UploadRuntimeInputParams) (dir, name string, info os.FileInfo, err error) {
	localPath := strings.TrimSpace(params.LocalPath)
	if localPath == "" {
		return "", "", nil, fmt.Errorf("local path is required")
	}
	remotePath := strings.TrimSpace(params.RemotePath)
	if remotePath == "" {
		return "", "", nil, fmt.Errorf("remote path is required")
	}
	dir, name, err = splitRuntimeInputDestination(remotePath)
	if err != nil {
		return "", "", nil, err
	}
	info, err = os.Stat(localPath)
	if err != nil {
		return "", "", nil, fmt.Errorf("local file %q: %w", localPath, err)
	}
	if info.IsDir() {
		return "", "", nil, fmt.Errorf("local path %q is a directory; inputs upload transfers one file at a time", localPath)
	}
	return dir, name, info, nil
}

// verifyRuntimeInputUpload rejects a corrupted or partial write.
func verifyRuntimeInputUpload(target, localSHA string, localSize int64, stdout string) error {
	remoteSize, remoteSHA, err := parseRuntimeInputUploadResponse(stdout)
	if err != nil {
		return fmt.Errorf("upload runtime input %q: %w", target, err)
	}
	if !strings.EqualFold(remoteSHA, localSHA) {
		return fmt.Errorf("upload runtime input %q: checksum mismatch after transfer (local %s, remote %s)", target, localSHA, remoteSHA)
	}
	if remoteSize != localSize {
		return fmt.Errorf("upload runtime input %q: size mismatch after transfer (local %d bytes, remote %d bytes)", target, localSize, remoteSize)
	}
	return nil
}

func splitRuntimeInputDestination(remotePath string) (dir, name string, err error) {
	cleaned, err := core.ValidateAbsolutePodPath(remotePath, "remote path")
	if err != nil {
		return "", "", err
	}
	if cleaned == "/" {
		return "", "", fmt.Errorf("remote path must include a file name: %q", remotePath)
	}
	name, err = core.SanitizeOutputEntryName(path.Base(cleaned))
	if err != nil {
		return "", "", err
	}
	return path.Dir(cleaned), name, nil
}

// traceRuntimeInputUploadScript returns true in dry-run, when the caller must not run.
func traceRuntimeInputUploadScript(ctx core.Context, req config.ShellLaunchParams, label, script string) bool {
	traceArgs := append([]string{}, kubectlRemoteExecStdinArgs(req, script)...)
	if len(traceArgs) > 0 {
		traceArgs[len(traceArgs)-1] = "<remote-script>"
	}
	ctx.TraceCommand("", "kubectl", traceArgs...)
	ctx.TraceBlock(label, script)
	return ctx.DryRun
}

// runtimeInputUploadScript writes via temp file + rename so a killed transfer
// leaves nothing at the final path, and reports size and sha256.
func runtimeInputUploadScript(dir, name string) string {
	quotedDir := core.ShellQuote(dir)
	quotedName := core.ShellQuote(name)
	return strings.Join([]string{
		"set -e",
		"dir=" + quotedDir,
		"name=" + quotedName,
		"mkdir -p \"$dir\" 2>/dev/null || true",
		// Drain stdin before refusing: kubectl exec -i otherwise hangs until
		// every byte is accepted.
		"if [ ! -d \"$dir\" ] || [ ! -w \"$dir\" ]; then cat >/dev/null; echo \"erun-inputs: destination directory is not writable: $dir\" >&2; exit 3; fi",
		"tmp=\"$dir/.$name.erun-upload-tmp\"",
		"cat > \"$tmp\"",
		"mv \"$tmp\" \"$dir/$name\"",
		"size=$(stat -c %s \"$dir/$name\" 2>/dev/null || echo 0)",
		"sha=$(sha256sum \"$dir/$name\" 2>/dev/null | awk '{print $1}')",
		"printf '%s\\t%s\\n' \"$size\" \"$sha\"",
	}, "\n")
}

// parseRuntimeInputUploadResponse decodes the single "<size>\t<sha256>" line.
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

func kubectlRemoteExecStdinArgs(req config.ShellLaunchParams, script string) []string {
	args := config.KubectlTargetArgs(req)
	args = append(args, "exec", "-i", "deployment/"+config.RuntimeReleaseName(req.Tenant), "--", "/bin/sh", "-c", script)
	return args
}

// RunRemoteCommandWithStdin is the stdin-streaming counterpart of RunRemoteCommand.
func RunRemoteCommandWithStdin(req config.ShellLaunchParams, script string, stdin io.Reader) (kube.RemoteCommandResult, error) {
	cmd := core.Command("kubectl", kubectlRemoteExecStdinArgs(req, script)...)
	cmd.Stdin = stdin
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return kube.RemoteCommandResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}, err
}
