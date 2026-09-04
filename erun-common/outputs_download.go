package eruncommon

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
)

// runtimeOutputChunkBytes bounds how much payload one `kubectl exec` carries.
// The exec stream does not break at a size — it breaks probabilistically as a
// single transfer's volume and duration grow, so a whole-file stream fails more
// and more often the bigger the file is and essentially never completes for a
// real cross-built binary. Reading the payload as a series of bounded ranges
// keeps every stream inside the band that transfers reliably, whatever the
// file's size. 8 MiB of payload is at most ~11 MiB on the wire once base64
// expands it, below the largest transfers measured as reliable, and it is the
// worst case rather than the normal one because the pod compresses first.
const runtimeOutputChunkBytes = 8 * 1024 * 1024

// runtimeOutputChunkAttempts retries one range before the download gives up. A
// broken stream is a transport fault, not a fact about the bytes, so re-reading
// the same range is expected to succeed — and a range that keeps failing says
// the connection is at fault rather than the file.
const runtimeOutputChunkAttempts = 3

// runtimeOutputSource is what the pod reports about a payload before the rest of
// it moves: whether the entry was a directory (so the staged path is a throwaway
// archive to clean up), the exact length to expect, and the digest that proves
// the reassembled bytes are the pod's.
type runtimeOutputSource struct {
	IsDir  bool
	Path   string
	Size   int64
	SHA256 string
}

// DownloadRuntimeOutput fetches one entry from the env's runtime pod as bytes;
// a directory arrives as a gzip tarball (IsArchive=true). The payload is read as
// a series of bounded ranges rather than one stream, and the reassembled bytes
// are checked against the pod's own digest. Dry-run returns a preview result
// with no bytes and no transfer.
func DownloadRuntimeOutput(ctx Context, req ShellLaunchParams, params RuntimeOutputDownloadParams, run RuntimeOutputsRunner) (RuntimeOutputResult, error) {
	dir, err := resolveOutputsDir(params.Dir)
	if err != nil {
		return RuntimeOutputResult{}, err
	}
	name, err := sanitizeOutputEntryName(params.Name)
	if err != nil {
		return RuntimeOutputResult{}, err
	}
	if run == nil {
		run = RunRemoteCommand
	}
	target := path.Join(dir, name)
	ctx.Trace("outputs: downloading " + target)
	probe := runtimeOutputProbeScript(dir, name)
	traceRuntimeOutputsScript(ctx, req, "outputs probe script", probe)
	traceRuntimeOutputsScript(ctx, req, "outputs range script", runtimeOutputRangeScript(target, 0, runtimeOutputChunkBytes))
	if ctx.DryRun {
		return RuntimeOutputResult{Name: name}, nil
	}
	return fetchRuntimeOutput(ctx, req, name, probe, run)
}

// fetchRuntimeOutput asks the pod what it would send, reads it, and refuses a
// payload the pod does not vouch for.
func fetchRuntimeOutput(ctx Context, req ShellLaunchParams, name, probe string, run RuntimeOutputsRunner) (RuntimeOutputResult, error) {
	out, err := run(req, probe)
	if err != nil {
		return RuntimeOutputResult{}, fmt.Errorf("download runtime output %q%s: %w", name, formatRemoteCommandStderr(out.Stderr), err)
	}
	source, head, err := parseRuntimeOutputProbe(name, out.Stdout)
	if err != nil {
		return RuntimeOutputResult{}, err
	}
	if source.IsDir {
		defer discardRuntimeOutputStagedArchive(ctx, req, source.Path, run)
	}
	data, err := transferRuntimeOutput(ctx, req, name, source, head, run)
	if err != nil {
		return RuntimeOutputResult{}, err
	}
	result := newRuntimeOutputResult(name, source.IsDir, data)
	if source.SHA256 != "" && !strings.EqualFold(source.SHA256, result.SHA256) {
		return RuntimeOutputResult{}, fmt.Errorf("download runtime output %q: the %d transferred bytes hash to %s but the pod reported %s", name, result.Size, result.SHA256, source.SHA256)
	}
	return result, nil
}

// transferRuntimeOutput assembles the payload from the range the probe already
// sent plus however many more it takes, and reports how far it got when a range
// cannot be read: the size of the transfer is what makes it fail, so an operator
// must never have to infer that from a bare stream error.
func transferRuntimeOutput(ctx Context, req ShellLaunchParams, name string, source runtimeOutputSource, head []byte, run RuntimeOutputsRunner) ([]byte, error) {
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

// readRuntimeOutputRange reads one range, retrying a failed or short read. A
// range whose decoded length is not the requested one is treated as a failed
// read, so a truncated stream can never be assembled into a plausible file.
func readRuntimeOutputRange(ctx Context, req ShellLaunchParams, sourcePath string, offset, length int64, run RuntimeOutputsRunner) ([]byte, error) {
	script := runtimeOutputRangeScript(sourcePath, offset, length)
	var last error
	for attempt := 1; attempt <= runtimeOutputChunkAttempts; attempt++ {
		out, err := run(req, script)
		if err != nil {
			last = fmt.Errorf("read bytes %d-%d%s: %w", offset, offset+length-1, formatRemoteCommandStderr(out.Stderr), err)
		} else if chunk, decodeErr := decodeRuntimeOutputChunk(out.Stdout, length); decodeErr != nil {
			last = fmt.Errorf("read bytes %d-%d: %w", offset, offset+length-1, decodeErr)
		} else {
			return chunk, nil
		}
		ctx.Trace(fmt.Sprintf("outputs: the range at offset %d failed on attempt %d of %d: %v", offset, attempt, runtimeOutputChunkAttempts, last))
	}
	return nil, last
}

// decodeRuntimeOutputChunk decodes one range response: an encoding marker line
// followed by the base64 body. The marker is what lets the pod compress when it
// can without the caller having to guess whether it did.
func decodeRuntimeOutputChunk(stdout string, length int64) ([]byte, error) {
	marker, body, _ := strings.Cut(strings.TrimLeft(stdout, "\n"), "\n")
	marker = strings.TrimSpace(marker)
	if marker != "gzip" && marker != "raw" {
		return nil, fmt.Errorf("the pod did not say how it encoded the range")
	}
	// base64(1) wraps at 76 columns, so the body arrives as thousands of lines.
	// Stripping them in one pass matters at these sizes: splitting on whitespace
	// and rejoining walks and reallocates the whole megabyte-scale range twice.
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

// discardRuntimeOutputStagedArchive removes the archive the probe staged for a
// directory download. A leftover staging file is the pod's problem to carry, so
// it is removed even when the transfer failed — and a failed removal is only
// traced, because the bytes the caller asked for have already arrived.
func discardRuntimeOutputStagedArchive(ctx Context, req ShellLaunchParams, stagedPath string, run RuntimeOutputsRunner) {
	script := "rm -f " + shellQuote(stagedPath)
	ctx.TraceBlock("outputs discard staged archive", script)
	if _, err := run(req, script); err != nil {
		ctx.Trace(fmt.Sprintf("outputs: could not remove the staged archive %s: %v", stagedPath, err))
	}
}

// parseRuntimeOutputProbe reads the probe's answer: four metadata lines — the
// entry kind, the payload length, the path to read it from, and its digest
// (empty when the pod has no hashing tool) — followed by the payload's first
// range, so a download that fits in one range costs one round trip. The kind
// line anchors the parse so a login shell's own banner cannot shift it.
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

// runtimeOutputProbeScript resolves the entry, stages a directory as a gzip
// tarball so its bytes stay fixed while they are read range by range, reports
// the length and digest of whatever will be sent, and sends the first range with
// it. /dev, /proc, and /sys are excluded defensively from the archive.
func runtimeOutputProbeScript(dir, name string) string {
	quotedDir := shellQuote(dir)
	quotedName := shellQuote(name)
	target := shellQuote(path.Join(dir, name))
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

// runtimeOutputRangeScript reads one byte range of the staged payload.
func runtimeOutputRangeScript(sourcePath string, offset, length int64) string {
	return runtimeOutputRangePipeline(shellQuote(sourcePath), offset, length)
}

// runtimeOutputRangePipeline emits the range read itself, announcing whether it
// compressed the bytes first. tail and head seek a regular file, so a late range
// costs no more than an early one; gzip is what keeps a compressible payload's
// stream far below the size where the stream itself becomes the risk, and its
// absence has to stay survivable because a download must not depend on it.
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
