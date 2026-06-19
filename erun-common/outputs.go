package eruncommon

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// DefaultRuntimeOutputsDir is the canonical agent outputs directory inside the
// runtime pod. Agents and skills write deliverables there; `erun outputs
// list`/`download` read from it. The runtime chart exports ERUN_OUTPUTS_DIR
// with this value and the image creates it; this constant is the host-side
// default when no --path override is given (the CLI runs off the pod and can't
// read the pod's env).
const DefaultRuntimeOutputsDir = "/home/erun/.erun/outputs"

// MaxRuntimeOutputBytes caps a single download. The transfer base64-encodes the
// whole payload over one `kubectl exec` and buffers it in memory, so an
// unbounded download could exhaust memory; reject larger entries with a clear
// error instead.
const MaxRuntimeOutputBytes = 100 * 1024 * 1024

// outputDownloadArchiveFormat is the archive format directories download as.
const outputDownloadArchiveFormat = "tar.gz"

// OutputEntry is one file or directory in the agent outputs directory.
type OutputEntry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
	IsDir   bool      `json:"isDir"`
}

// RuntimeOutputsListResult is the read-model for `outputs list`: the entries in
// one pod directory, newest-first.
type RuntimeOutputsListResult struct {
	Dir       string        `json:"dir"`
	Entries   []OutputEntry `json:"entries"`
	Total     int           `json:"total"`
	Truncated bool          `json:"truncated"`
}

// RuntimeOutputResult is the result of `outputs download`: one entry's bytes,
// plus the metadata a caller needs to save them. For a directory the bytes are
// a gzip-compressed tarball (IsArchive=true). Bytes is excluded from JSON; a
// transport that returns the payload inline (MCP) base64-encodes it separately.
type RuntimeOutputResult struct {
	Name          string `json:"name"`
	IsArchive     bool   `json:"isArchive"`
	ArchiveFormat string `json:"archiveFormat,omitempty"`
	Size          int64  `json:"size"`
	SHA256        string `json:"sha256"`
	Bytes         []byte `json:"-"`
}

// RuntimeOutputsParams selects which directory `outputs list` reads.
type RuntimeOutputsParams struct {
	// Dir is the pod directory to list; empty defaults to DefaultRuntimeOutputsDir.
	Dir string
	// Limit caps the number of entries returned (newest-first). 0 means no limit.
	Limit int
}

// RuntimeOutputDownloadParams selects which entry `outputs download` fetches.
type RuntimeOutputDownloadParams struct {
	// Dir is the base directory the entry lives under; empty defaults to
	// DefaultRuntimeOutputsDir.
	Dir string
	// Name is the entry to download, a single path segment under Dir.
	Name string
}

// RuntimeOutputsRunner runs a /bin/sh script in the env's runtime pod and
// returns its captured output. RunRemoteCommand is the production implementation;
// it is a parameter so the CLI and tests can inject a seam.
type RuntimeOutputsRunner func(req ShellLaunchParams, script string) (RemoteCommandResult, error)

// resolveOutputsDir returns the directory to operate on, defaulting to the
// canonical outputs dir, and validates it is a clean absolute path with no
// parent-traversal segments.
func resolveOutputsDir(dir string) (string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return DefaultRuntimeOutputsDir, nil
	}
	if !strings.HasPrefix(dir, "/") {
		return "", fmt.Errorf("outputs path must be absolute: %q", dir)
	}
	cleaned := path.Clean(dir)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, "/../") {
		return "", fmt.Errorf("outputs path must not contain '..': %q", dir)
	}
	return cleaned, nil
}

// sanitizeOutputEntryName reduces an entry name to a single safe path segment:
// directory components (POSIX or Windows) are stripped and "."/".."/empty are
// rejected, so path.Join with it can never escape the base directory.
func sanitizeOutputEntryName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if i := strings.LastIndexAny(trimmed, "/\\"); i >= 0 {
		trimmed = trimmed[i+1:]
	}
	trimmed = strings.TrimSpace(trimmed)
	if trimmed == "" || trimmed == "." || trimmed == ".." {
		return "", fmt.Errorf("invalid outputs entry name: %q", name)
	}
	return trimmed, nil
}

// ResolveRuntimeOutputs lists one directory in the env's runtime pod,
// newest-first. A missing directory yields an empty result, not an error. In
// dry-run it traces the kubectl exec and the listing script and returns an
// empty result without contacting the pod.
func ResolveRuntimeOutputs(ctx Context, req ShellLaunchParams, params RuntimeOutputsParams, run RuntimeOutputsRunner) (RuntimeOutputsListResult, error) {
	dir, err := resolveOutputsDir(params.Dir)
	if err != nil {
		return RuntimeOutputsListResult{}, err
	}
	if run == nil {
		run = RunRemoteCommand
	}
	ctx.Trace("outputs: listing " + dir)
	script := runtimeOutputsListScript(dir)
	if traced := traceRuntimeOutputsScript(ctx, req, "outputs list script", script); traced {
		return RuntimeOutputsListResult{Dir: dir}, nil
	}
	out, err := run(req, script)
	if err != nil {
		return RuntimeOutputsListResult{}, fmt.Errorf("list runtime outputs%s: %w", formatRemoteCommandStderr(out.Stderr), err)
	}
	entries := parseRuntimeOutputsListing(out.Stdout)
	for i := range entries {
		entries[i].Path = path.Join(dir, entries[i].Name)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].ModTime.After(entries[j].ModTime)
	})
	result := RuntimeOutputsListResult{Dir: dir, Total: len(entries), Entries: entries}
	if params.Limit > 0 && len(entries) > params.Limit {
		result.Entries = entries[:params.Limit]
		result.Truncated = true
	}
	return result, nil
}

// DownloadRuntimeOutput fetches one entry from the env's runtime pod as bytes.
// A file is base64-streamed; a directory is gzip-tarred then base64-streamed
// (IsArchive=true). The payload is SHA-256'd and size-capped. In dry-run it
// traces the kubectl exec and the transfer script and returns a preview result
// (no bytes, no transfer).
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
	script := runtimeOutputDownloadScript(dir, name)
	if traced := traceRuntimeOutputsScript(ctx, req, "outputs download script", script); traced {
		return RuntimeOutputResult{Name: name}, nil
	}
	out, err := run(req, script)
	if err != nil {
		return RuntimeOutputResult{}, fmt.Errorf("download runtime output %q%s: %w", name, formatRemoteCommandStderr(out.Stderr), err)
	}
	kind, encoded, ok := strings.Cut(strings.TrimSpace(out.Stdout), "\n")
	if !ok && kind == "" {
		return RuntimeOutputResult{}, fmt.Errorf("download runtime output %q: empty response", name)
	}
	isDir := strings.TrimSpace(kind) == "dir"
	data, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(encoded), ""))
	if err != nil {
		return RuntimeOutputResult{}, fmt.Errorf("decode runtime output %q: %w", name, err)
	}
	if len(data) > MaxRuntimeOutputBytes {
		return RuntimeOutputResult{}, fmt.Errorf("runtime output %q is too large (%d bytes); the limit is %d bytes", name, len(data), MaxRuntimeOutputBytes)
	}
	sum := sha256.Sum256(data)
	result := RuntimeOutputResult{
		Name:   name,
		Size:   int64(len(data)),
		SHA256: hex.EncodeToString(sum[:]),
		Bytes:  data,
	}
	if isDir {
		result.IsArchive = true
		result.ArchiveFormat = outputDownloadArchiveFormat
		result.Name = name + "." + outputDownloadArchiveFormat
	}
	return result, nil
}

// traceRuntimeOutputsScript traces the kubectl exec argv (with the script body
// redacted to a placeholder) plus the script itself, mirroring the bootstrap
// remote-script trace. It returns true when the context is in dry-run, in which
// case the caller must not run the command.
func traceRuntimeOutputsScript(ctx Context, req ShellLaunchParams, label, script string) bool {
	traceArgs := append([]string{}, kubectlRemoteExecArgs(req, script)...)
	if len(traceArgs) > 0 {
		traceArgs[len(traceArgs)-1] = "<remote-script>"
	}
	ctx.TraceCommand("", "kubectl", traceArgs...)
	ctx.TraceBlock(label, script)
	return ctx.DryRun
}

// runtimeOutputsListScript lists one directory one level deep, emitting a
// tab-separated record per entry: type (f/d), size, mtime epoch, basename. A
// missing directory prints nothing (empty result, not an error).
func runtimeOutputsListScript(dir string) string {
	quoted := shellQuote(dir)
	return fmt.Sprintf("if [ -d %s ]; then find %s -mindepth 1 -maxdepth 1 -printf '%%y\\t%%s\\t%%T@\\t%%f\\n'; fi", quoted, quoted)
}

// runtimeOutputDownloadScript streams one entry: a directory as a gzip tarball,
// a file raw, each base64-encoded after a single type-marker line. /dev, /proc,
// and /sys are excluded defensively from the archive.
func runtimeOutputDownloadScript(dir, name string) string {
	quotedDir := shellQuote(dir)
	quotedName := shellQuote(name)
	target := shellQuote(path.Join(dir, name))
	limit := strconv.Itoa(MaxRuntimeOutputBytes)
	return strings.Join([]string{
		"target=" + target,
		"if [ -d \"$target\" ]; then",
		"  printf 'dir\\n'",
		"  tar czf - --exclude=/dev --exclude=/proc --exclude=/sys -C " + quotedDir + " " + quotedName + " | base64",
		"elif [ -f \"$target\" ]; then",
		"  size=$(stat -c %s \"$target\" 2>/dev/null || echo 0)",
		"  if [ \"$size\" -gt " + limit + " ]; then echo \"erun-outputs: file exceeds size limit\" >&2; exit 4; fi",
		"  printf 'file\\n'",
		"  base64 \"$target\"",
		"else",
		"  echo \"erun-outputs: not found: $target\" >&2",
		"  exit 3",
		"fi",
	}, "\n")
}

// parseRuntimeOutputsListing parses the tab-separated find output into entries.
// Malformed lines are skipped rather than failing the whole listing.
func parseRuntimeOutputsListing(stdout string) []OutputEntry {
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	entries := make([]OutputEntry, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 4)
		if len(fields) != 4 {
			continue
		}
		size, _ := strconv.ParseInt(fields[1], 10, 64)
		name := fields[3]
		entries = append(entries, OutputEntry{
			Name:    name,
			Size:    size,
			ModTime: parseFindEpoch(fields[2]),
			IsDir:   fields[0] == "d",
		})
	}
	return entries
}

// ResolveLocalOutputs lists one directory on the local filesystem, newest-first.
// It is the in-pod counterpart of ResolveRuntimeOutputs: the MCP server runs
// inside the runtime pod, co-located with the files, so it reads the directory
// directly instead of exec-ing into it. A missing directory yields an empty
// result, not an error.
func ResolveLocalOutputs(params RuntimeOutputsParams) (RuntimeOutputsListResult, error) {
	dir, err := resolveOutputsDir(params.Dir)
	if err != nil {
		return RuntimeOutputsListResult{}, err
	}
	result := RuntimeOutputsListResult{Dir: dir}
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return RuntimeOutputsListResult{}, err
	}
	entries := make([]OutputEntry, 0, len(dirEntries))
	for _, e := range dirEntries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		entries = append(entries, OutputEntry{
			Name:    e.Name(),
			Path:    filepath.Join(dir, e.Name()),
			Size:    info.Size(),
			ModTime: info.ModTime().UTC(),
			IsDir:   e.IsDir(),
		})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].ModTime.After(entries[j].ModTime)
	})
	result.Total = len(entries)
	result.Entries = entries
	if params.Limit > 0 && len(entries) > params.Limit {
		result.Entries = entries[:params.Limit]
		result.Truncated = true
	}
	return result, nil
}

// StatLocalOutput resolves one local entry and returns its metadata without
// reading any bytes — the in-pod preview path for outputs_download. A directory
// is reported as the archive it would produce.
func StatLocalOutput(params RuntimeOutputDownloadParams) (RuntimeOutputResult, error) {
	_, name, target, err := resolveLocalOutputTarget(params)
	if err != nil {
		return RuntimeOutputResult{}, err
	}
	info, err := statLocalTarget(target, name)
	if err != nil {
		return RuntimeOutputResult{}, err
	}
	result := RuntimeOutputResult{Name: name}
	if info.IsDir() {
		result.IsArchive = true
		result.ArchiveFormat = outputDownloadArchiveFormat
		result.Name = name + "." + outputDownloadArchiveFormat
		return result, nil
	}
	result.Size = info.Size()
	return result, nil
}

// DownloadLocalOutput reads one local entry into memory — the in-pod counterpart
// of DownloadRuntimeOutput. A file is read raw; a directory becomes an in-memory
// gzip tarball (IsArchive=true). The payload is SHA-256'd and size-capped.
func DownloadLocalOutput(params RuntimeOutputDownloadParams) (RuntimeOutputResult, error) {
	dir, name, target, err := resolveLocalOutputTarget(params)
	if err != nil {
		return RuntimeOutputResult{}, err
	}
	info, err := statLocalTarget(target, name)
	if err != nil {
		return RuntimeOutputResult{}, err
	}
	if info.IsDir() {
		data, err := tarGzLocalDir(dir, name)
		if err != nil {
			return RuntimeOutputResult{}, err
		}
		if len(data) > MaxRuntimeOutputBytes {
			return RuntimeOutputResult{}, fmt.Errorf("runtime output %q is too large (%d bytes); the limit is %d bytes", name, len(data), MaxRuntimeOutputBytes)
		}
		sum := sha256.Sum256(data)
		return RuntimeOutputResult{
			Name:          name + "." + outputDownloadArchiveFormat,
			IsArchive:     true,
			ArchiveFormat: outputDownloadArchiveFormat,
			Size:          int64(len(data)),
			SHA256:        hex.EncodeToString(sum[:]),
			Bytes:         data,
		}, nil
	}
	if info.Size() > MaxRuntimeOutputBytes {
		return RuntimeOutputResult{}, fmt.Errorf("runtime output %q is too large (%d bytes); the limit is %d bytes", name, info.Size(), MaxRuntimeOutputBytes)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return RuntimeOutputResult{}, err
	}
	sum := sha256.Sum256(data)
	return RuntimeOutputResult{
		Name:   name,
		Size:   int64(len(data)),
		SHA256: hex.EncodeToString(sum[:]),
		Bytes:  data,
	}, nil
}

func resolveLocalOutputTarget(params RuntimeOutputDownloadParams) (dir, name, target string, err error) {
	dir, err = resolveOutputsDir(params.Dir)
	if err != nil {
		return "", "", "", err
	}
	name, err = sanitizeOutputEntryName(params.Name)
	if err != nil {
		return "", "", "", err
	}
	return dir, name, filepath.Join(dir, name), nil
}

func statLocalTarget(target, name string) (os.FileInfo, error) {
	info, err := os.Stat(target)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("runtime output not found: %s", name)
		}
		return nil, err
	}
	return info, nil
}

// tarGzLocalDir builds an in-memory gzip tarball of one directory, with entry
// names relative to its parent so the archive unpacks as <name>/…. Symlinks are
// stored as links; only regular files carry content.
func tarGzLocalDir(parent, name string) ([]byte, error) {
	root := filepath.Join(parent, name)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	walkErr := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(parent, p)
		if err != nil {
			return err
		}
		link := ""
		if info.Mode()&os.ModeSymlink != 0 {
			if link, err = os.Readlink(p); err != nil {
				return err
			}
		}
		hdr, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		if info.IsDir() {
			hdr.Name += "/"
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		_, err = io.Copy(tw, f)
		return err
	})
	if walkErr != nil {
		return nil, walkErr
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// parseFindEpoch parses find's `%T@` mtime (`<seconds>.<nanos>`) into a UTC
// time. A malformed value yields the zero time, which sorts last.
func parseFindEpoch(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	secStr, fracStr, _ := strings.Cut(value, ".")
	sec, err := strconv.ParseInt(secStr, 10, 64)
	if err != nil {
		return time.Time{}
	}
	var nsec int64
	if fracStr != "" {
		if len(fracStr) > 9 {
			fracStr = fracStr[:9]
		}
		fracStr += strings.Repeat("0", 9-len(fracStr))
		nsec, _ = strconv.ParseInt(fracStr, 10, 64)
	}
	return time.Unix(sec, nsec).UTC()
}
