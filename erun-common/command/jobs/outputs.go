package jobs

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
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

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/codesign"
	"github.com/sophium/erun/erun-common/infra/kube"
)

// MaxRuntimeOutputBytes caps a single download, which is buffered in memory.
const MaxRuntimeOutputBytes = 100 * 1024 * 1024

const outputDownloadArchiveFormat = "tar.gz"

type OutputEntry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
	IsDir   bool      `json:"isDir"`
}

// RuntimeOutputsListResult is one pod directory's entries, newest-first.
type RuntimeOutputsListResult struct {
	Dir       string        `json:"dir"`
	Entries   []OutputEntry `json:"entries"`
	Total     int           `json:"total"`
	Truncated bool          `json:"truncated"`
}

// RuntimeOutputResult is one downloaded entry; a directory arrives as a gzip
// tarball. Bytes stays out of JSON so transports can encode it separately.
type RuntimeOutputResult struct {
	Name          string `json:"name"`
	IsArchive     bool   `json:"isArchive"`
	ArchiveFormat string `json:"archiveFormat,omitempty"`
	Size          int64  `json:"size"`
	SHA256        string `json:"sha256"`
	Bytes         []byte `json:"-"`
	// Signing is nil unless host-side ad-hoc signing had something to report.
	Signing *codesign.HostArtifactSigning `json:"signing,omitempty"`
}

// RuntimeOutputsParams selects which directory `outputs list` reads.
type RuntimeOutputsParams struct {
	// Dir defaults to DefaultRuntimeOutputsDir.
	Dir string
	// Limit caps entries returned; 0 means no limit.
	Limit int
}

// RuntimeOutputDownloadParams selects which entry `outputs download` fetches.
type RuntimeOutputDownloadParams struct {
	// Dir defaults to DefaultRuntimeOutputsDir.
	Dir string
	// Name is a single path segment under Dir.
	Name string
}

// RuntimeOutputsRunner runs a /bin/sh script in the env's runtime pod.
type RuntimeOutputsRunner func(req config.ShellLaunchParams, script string) (kube.RemoteCommandResult, error)

func resolveOutputsDir(dir string) (string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return core.DefaultRuntimeOutputsDir, nil
	}
	return core.ValidateAbsolutePodPath(dir, "outputs path")
}

// ResolveRuntimeOutputs lists one runtime-pod directory, newest-first; a missing
// directory is an empty result.
func ResolveRuntimeOutputs(ctx core.Context, req config.ShellLaunchParams, params RuntimeOutputsParams, run RuntimeOutputsRunner) (RuntimeOutputsListResult, error) {
	dir, err := resolveOutputsDir(params.Dir)
	if err != nil {
		return RuntimeOutputsListResult{}, err
	}
	if run == nil {
		run = kube.RunRemoteCommand
	}
	ctx.Trace("outputs: listing " + dir)
	script := runtimeOutputsListScript(dir)
	if traced := traceRuntimeOutputsScript(ctx, req, "outputs list script", script); traced {
		return RuntimeOutputsListResult{Dir: dir}, nil
	}
	out, err := run(req, script)
	if err != nil {
		return RuntimeOutputsListResult{}, fmt.Errorf("list runtime outputs%s: %w", kube.FormatRemoteCommandStderr(out.Stderr), err)
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

func newRuntimeOutputResult(name string, isDir bool, data []byte) RuntimeOutputResult {
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
	return result
}

// traceRuntimeOutputsScript reports dry-run, in which case the caller must not run.
func traceRuntimeOutputsScript(ctx core.Context, req config.ShellLaunchParams, label, script string) bool {
	traceArgs := append([]string{}, kube.KubectlRemoteExecArgs(req, script)...)
	if len(traceArgs) > 0 {
		traceArgs[len(traceArgs)-1] = "<remote-script>"
	}
	ctx.TraceCommand("", "kubectl", traceArgs...)
	ctx.TraceBlock(label, script)
	return ctx.DryRun
}

// runtimeOutputsListScript emits "type\tsize\tmtime\tname" per entry.
func runtimeOutputsListScript(dir string) string {
	quoted := core.ShellQuote(dir)
	return fmt.Sprintf("if [ -d %s ]; then find %s -mindepth 1 -maxdepth 1 -printf '%%y\\t%%s\\t%%T@\\t%%f\\n'; fi", quoted, quoted)
}

// parseRuntimeOutputsListing skips malformed lines.
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

// ResolveLocalOutputs is ResolveRuntimeOutputs for the in-pod MCP server, reading
// the filesystem directly.
func ResolveLocalOutputs(params RuntimeOutputsParams) (RuntimeOutputsListResult, error) {
	dir, err := resolveOutputsDir(params.Dir)
	if err != nil {
		return RuntimeOutputsListResult{}, err
	}
	result := RuntimeOutputsListResult{Dir: dir, Entries: []OutputEntry{}}
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

// StatLocalOutput returns an entry's metadata without reading its bytes.
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

// DownloadLocalOutput is DownloadRuntimeOutput for the in-pod path.
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
		return newRuntimeOutputResult(name, true, data), nil
	}
	if info.Size() > MaxRuntimeOutputBytes {
		return RuntimeOutputResult{}, fmt.Errorf("runtime output %q is too large (%d bytes); the limit is %d bytes", name, info.Size(), MaxRuntimeOutputBytes)
	}
	// Sign before reading so the returned bytes carry the ad-hoc signature.
	signing := codesign.SignHostArtifact(target)
	data, err := os.ReadFile(target)
	if err != nil {
		return RuntimeOutputResult{}, err
	}
	result := newRuntimeOutputResult(name, false, data)
	if signing.Describe() != "" {
		result.Signing = &signing
	}
	return result, nil
}

func resolveLocalOutputTarget(params RuntimeOutputDownloadParams) (dir, name, target string, err error) {
	dir, err = resolveOutputsDir(params.Dir)
	if err != nil {
		return "", "", "", err
	}
	name, err = core.SanitizeOutputEntryName(params.Name)
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

// tarGzLocalDir tars a directory with names relative to parent, unpacking as <name>/.
func tarGzLocalDir(parent, name string) ([]byte, error) {
	root := filepath.Join(parent, name)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	walkErr := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return tarGzWriteEntry(tw, parent, p, info)
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

func tarGzWriteEntry(tw *tar.Writer, parent, p string, info os.FileInfo) error {
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
}

// parseFindEpoch parses find's `%T@` mtime; malformed yields the zero time.
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
