package jobs

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/kube"
)

// runtimeOutputChunkBytes bounds one `kubectl exec` transfer: exec streams fail
// more often as size grows, so payloads move as bounded ranges (~11 MiB base64 worst case).
const runtimeOutputChunkBytes = 8 * 1024 * 1024

// runtimeOutputChunkAttempts retries one range; a broken stream is a transport fault.
const runtimeOutputChunkAttempts = 3

// runtimeOutputSource is the pod's description of a payload before it moves.
type runtimeOutputSource struct {
	IsDir  bool
	Path   string
	Size   int64
	SHA256 string
}

// DownloadRuntimeOutput fetches one runtime-pod entry in bounded ranges and
// checks it against the pod's digest; a directory arrives as a gzip tarball.
func DownloadRuntimeOutput(ctx core.Context, req config.ShellLaunchParams, params model.RuntimeOutputDownloadParams, run RuntimeOutputsRunner) (model.RuntimeOutputResult, error) {
	dir, err := resolveOutputsDir(params.Dir)
	if err != nil {
		return model.RuntimeOutputResult{}, err
	}
	name, err := core.SanitizeOutputEntryName(params.Name)
	if err != nil {
		return model.RuntimeOutputResult{}, err
	}
	if run == nil {
		run = kube.RunRemoteCommand
	}
	target := path.Join(dir, name)
	ctx.Trace("outputs: downloading " + target)
	probe := runtimeOutputProbeScript(dir, name)
	traceRuntimeOutputsScript(ctx, req, "outputs probe script", probe)
	traceRuntimeOutputsScript(ctx, req, "outputs range script", runtimeOutputRangeScript(target, 0, runtimeOutputChunkBytes))
	if ctx.DryRun {
		return model.RuntimeOutputResult{Name: name}, nil
	}
	return fetchRuntimeOutput(ctx, req, name, probe, run)
}

func fetchRuntimeOutput(ctx core.Context, req config.ShellLaunchParams, name, probe string, run RuntimeOutputsRunner) (model.RuntimeOutputResult, error) {
	out, err := run(req, probe)
	if err != nil {
		return model.RuntimeOutputResult{}, fmt.Errorf("download runtime output %q%s: %w", name, kube.FormatRemoteCommandStderr(out.Stderr), err)
	}
	source, head, err := parseRuntimeOutputProbe(name, out.Stdout)
	if err != nil {
		return model.RuntimeOutputResult{}, err
	}
	if source.IsDir {
		defer discardRuntimeOutputStagedArchive(ctx, req, source.Path, run)
	}
	data, err := transferRuntimeOutput(ctx, req, name, source, head, run)
	if err != nil {
		return model.RuntimeOutputResult{}, err
	}
	result := newRuntimeOutputResult(name, source.IsDir, data)
	if source.SHA256 != "" && !strings.EqualFold(source.SHA256, result.SHA256) {
		return model.RuntimeOutputResult{}, fmt.Errorf("download runtime output %q: the %d transferred bytes hash to %s but the pod reported %s", name, result.Size, result.SHA256, source.SHA256)
	}
	return result, nil
}

// transferRuntimeOutput reports how far it got when a range fails.
func transferRuntimeOutput(ctx core.Context, req config.ShellLaunchParams, name string, source runtimeOutputSource, head []byte, run RuntimeOutputsRunner) ([]byte, error) {
	if source.Size > MaxRuntimeOutputBytes {
		return nil, fmt.Errorf("runtime output %q is too large (%d bytes); the limit is %d bytes", name, source.Size, MaxRuntimeOutputBytes)
	}
	if source.Size > runtimeOutputChunkBytes {
		ctx.Trace(fmt.Sprintf("outputs: transferring %d bytes in ranges of up to %d", source.Size, runtimeOutputChunkBytes))
	}
	data := make([]byte, 0, source.Size)
	data = append(data, head...)
	for offset := int64(len(data)); offset < source.Size; offset = int64(len(data)) {
		length := source.Size - offset
		if length > runtimeOutputChunkBytes {
			length = runtimeOutputChunkBytes
		}
		chunk, err := readRuntimeOutputRange(ctx, req, source.Path, offset, length, run)
		if err != nil {
			return nil, fmt.Errorf("download runtime output %q: transferred %d of %d bytes: %w", name, offset, source.Size, err)
		}
		data = append(data, chunk...)
	}
	if int64(len(data)) != source.Size {
		return nil, fmt.Errorf("download runtime output %q: assembled %d bytes but the pod reported %d", name, len(data), source.Size)
	}
	return data, nil
}

// readRuntimeOutputRange retries a failed or short read, so a truncated stream
// is never assembled.
func readRuntimeOutputRange(ctx core.Context, req config.ShellLaunchParams, sourcePath string, offset, length int64, run RuntimeOutputsRunner) ([]byte, error) {
	script := runtimeOutputRangeScript(sourcePath, offset, length)
	var last error
	for attempt := 1; attempt <= runtimeOutputChunkAttempts; attempt++ {
		out, err := run(req, script)
		if err != nil {
			last = fmt.Errorf("read bytes %d-%d%s: %w", offset, offset+length-1, kube.FormatRemoteCommandStderr(out.Stderr), err)
		} else if chunk, decodeErr := decodeRuntimeOutputChunk(out.Stdout, length); decodeErr != nil {
			last = fmt.Errorf("read bytes %d-%d: %w", offset, offset+length-1, decodeErr)
		} else {
			return chunk, nil
		}
		ctx.Trace(fmt.Sprintf("outputs: the range at offset %d failed on attempt %d of %d: %v", offset, attempt, runtimeOutputChunkAttempts, last))
	}
	return nil, last
}

// decodeRuntimeOutputChunk decodes an encoding marker line ("gzip"/"raw") and a base64 body.
func decodeRuntimeOutputChunk(stdout string, length int64) ([]byte, error) {
	marker, body, _ := strings.Cut(strings.TrimLeft(stdout, "\n"), "\n")
	marker = strings.TrimSpace(marker)
	if marker != "gzip" && marker != "raw" {
		return nil, fmt.Errorf("the pod did not say how it encoded the range")
	}
	// One-pass strip: base64(1) wraps lines and ranges are megabytes.
	data, err := base64.StdEncoding.DecodeString(strings.Map(dropBase64Whitespace, body))
	if err != nil {
		return nil, fmt.Errorf("decode the pod's response: %w", err)
	}
	if marker == "gzip" {
		if data, err = gunzipRuntimeOutputChunk(data); err != nil {
			return nil, err
		}
	}
	if int64(len(data)) != length {
		return nil, fmt.Errorf("the pod returned %d bytes", len(data))
	}
	return data, nil
}

func dropBase64Whitespace(r rune) rune {
	switch r {
	case '\n', '\r', ' ', '\t':
		return -1
	}
	return r
}

func gunzipRuntimeOutputChunk(data []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decompress the pod's response: %w", err)
	}
	defer func() { _ = reader.Close() }()
	plain, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("decompress the pod's response: %w", err)
	}
	return plain, nil
}

// discardRuntimeOutputStagedArchive removes a staged directory archive even after
// a failed transfer; a failed removal is only traced.
func discardRuntimeOutputStagedArchive(ctx core.Context, req config.ShellLaunchParams, stagedPath string, run RuntimeOutputsRunner) {
	script := "rm -f " + core.ShellQuote(stagedPath)
	ctx.TraceBlock("outputs discard staged archive", script)
	if _, err := run(req, script); err != nil {
		ctx.Trace(fmt.Sprintf("outputs: could not remove the staged archive %s: %v", stagedPath, err))
	}
}

// parseRuntimeOutputProbe reads kind, size, path, and digest lines, then the
// first range. The kind line anchors the parse against login-shell banners.
func parseRuntimeOutputProbe(name, stdout string) (runtimeOutputSource, []byte, error) {
	lines := strings.Split(strings.ReplaceAll(stdout, "\r\n", "\n"), "\n")
	for i, line := range lines {
		kind := strings.TrimSpace(line)
		if kind != "file" && kind != "dir" {
			continue
		}
		if len(lines) < i+4 {
			break
		}
		size, err := strconv.ParseInt(strings.TrimSpace(lines[i+1]), 10, 64)
		if err != nil {
			return runtimeOutputSource{}, nil, fmt.Errorf("download runtime output %q: the pod reported an unreadable size %q", name, strings.TrimSpace(lines[i+1]))
		}
		sourcePath := strings.TrimSpace(lines[i+2])
		if sourcePath == "" {
			break
		}
		source := runtimeOutputSource{IsDir: kind == "dir", Path: sourcePath, Size: size, SHA256: strings.TrimSpace(lines[i+3])}
		length := size
		if length > runtimeOutputChunkBytes {
			length = runtimeOutputChunkBytes
		}
		head, err := decodeRuntimeOutputChunk(strings.Join(lines[i+4:], "\n"), length)
		if err != nil {
			return runtimeOutputSource{}, nil, fmt.Errorf("download runtime output %q: read bytes 0-%d: %w", name, length-1, err)
		}
		return source, head, nil
	}
	return runtimeOutputSource{}, nil, fmt.Errorf("download runtime output %q: the pod did not report what it would send", name)
}

// runtimeOutputProbeScript stages a directory as a tarball so its bytes stay
// fixed across range reads, then reports metadata and the first range.
func runtimeOutputProbeScript(dir, name string) string {
	quotedDir := core.ShellQuote(dir)
	quotedName := core.ShellQuote(name)
	target := core.ShellQuote(path.Join(dir, name))
	limit := strconv.Itoa(MaxRuntimeOutputBytes)
	return strings.Join([]string{
		"target=" + target,
		"if [ -d \"$target\" ]; then",
		"  staged=$(mktemp \"${TMPDIR:-/tmp}/erun-outputs-XXXXXX\") || { echo \"erun-outputs: cannot stage an archive of $target\" >&2; exit 5; }",
		"  if ! tar czf \"$staged\" --exclude=/dev --exclude=/proc --exclude=/sys -C " + quotedDir + " " + quotedName + "; then",
		"    rm -f \"$staged\"",
		"    echo \"erun-outputs: cannot archive $target\" >&2",
		"    exit 5",
		"  fi",
		"  kind=dir",
		"elif [ -f \"$target\" ]; then",
		"  staged=\"$target\"",
		"  kind=file",
		"else",
		"  echo \"erun-outputs: not found: $target\" >&2",
		"  exit 3",
		"fi",
		"size=$(wc -c < \"$staged\" | tr -d ' ')",
		"if [ \"$size\" -gt " + limit + " ]; then",
		"  if [ \"$kind\" = dir ]; then rm -f \"$staged\"; fi",
		"  echo \"erun-outputs: $target exceeds the " + limit + " byte limit ($size bytes)\" >&2",
		"  exit 4",
		"fi",
		"if command -v sha256sum >/dev/null 2>&1; then",
		"  digest=$(sha256sum \"$staged\" | cut -d' ' -f1)",
		"elif command -v openssl >/dev/null 2>&1; then",
		"  digest=$(openssl dgst -sha256 \"$staged\" | sed 's/.*[ =]//')",
		"else",
		"  digest=",
		"fi",
		"printf '%s\\n%s\\n%s\\n%s\\n' \"$kind\" \"$size\" \"$staged\" \"$digest\"",
		runtimeOutputRangePipeline("\"$staged\"", 0, runtimeOutputChunkBytes),
	}, "\n")
}

func runtimeOutputRangeScript(sourcePath string, offset, length int64) string {
	return runtimeOutputRangePipeline(core.ShellQuote(sourcePath), offset, length)
}

// runtimeOutputRangePipeline gzips when available and falls back to raw.
func runtimeOutputRangePipeline(quotedSource string, offset, length int64) string {
	read := fmt.Sprintf("tail -c +%d %s | head -c %d", offset+1, quotedSource, length)
	return strings.Join([]string{
		"if command -v gzip >/dev/null 2>&1; then",
		"  printf 'gzip\\n'",
		"  " + read + " | gzip -1 | base64",
		"else",
		"  printf 'raw\\n'",
		"  " + read + " | base64",
		"fi",
	}, "\n")
}
