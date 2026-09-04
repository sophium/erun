// Command erun-podexec stands in for the runtime pod that `erun outputs
// download` reads through, and for the exec stream that carries the answer.
//
// A fixed-stdout stub cannot serve this path: the download asks the pod several
// different questions (what would you send, then one byte range at a time, then
// discard the staging file), and the whole point of the fix under test is that
// the answers differ per call. So this program answers each of erun's real
// scripts against a real directory tree, and — the part that makes it a
// regression test rather than a smoke test — refuses to carry more than
// --max-stream-bytes in one call, failing with the exact error kubectl reports
// when its exec stream breaks. A caller that asks for a whole large file in one
// call therefore fails here the same way it fails against a real cluster.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The scripts erun sends. Matching the real text (rather than a marker planted
// for the test) is deliberate: a change to what erun asks the pod shows up here
// as a loud "unrecognized script" instead of a silently passing scenario.
var (
	probeScript   = regexp.MustCompile(`(?m)^target='([^']*)'$`)
	rangeRequest  = regexp.MustCompile(`tail -c \+(\d+) (?:'([^']*)'|"([^"]*)") \| head -c (\d+)`)
	discardScript = regexp.MustCompile(`^rm -f '([^']*)'\s*$`)
)

// wholePayloadRequest is the script erun sent before it read a payload range by
// range: one exec for the entire file. It is still answered here so a scenario
// pointed at that erun fails for the reason the bug report gives — a stream that
// carried too much and died — rather than for a protocol the pod never saw.
const wholePayloadRequest = `base64 "$target"`

// streamEOF is what kubectl prints when the exec stream dies mid-transfer.
const streamEOF = `E0904 16:36:15.551881   90445 v2.go:167] "Unhandled Error" err="read message: unexpected EOF"
error: error reading from error stream: read message: unexpected EOF`

// tarEntryEpoch keeps a staged directory archive byte-identical across runs, so
// a scenario's reported archive size is a fact rather than a coin flip.
var tarEntryEpoch = time.Unix(1700000000, 0).UTC()

type options struct {
	root           string
	maxStreamBytes int64
	encoding       string
	failOnceAt     string
	failAlwaysAt   string
	statePath      string
}

func main() {
	var opts options
	flag.StringVar(&opts.root, "root", "", "directory that stands in for the pod's filesystem root")
	flag.Int64Var(&opts.maxStreamBytes, "max-stream-bytes", 0, "fail any single exec whose stdout would exceed this many bytes (0 means no limit)")
	flag.StringVar(&opts.encoding, "encoding", "gzip", "how the pod encodes a range: gzip or raw")
	flag.StringVar(&opts.failOnceAt, "fail-once-at", "", "comma-separated byte offsets whose first read breaks the stream")
	flag.StringVar(&opts.failAlwaysAt, "fail-always-at", "", "comma-separated byte offsets whose every read breaks the stream")
	flag.StringVar(&opts.statePath, "state", "", "file that records which fail-once offsets have already fired")
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		fail("podexec: no kubectl arguments were forwarded")
	}
	script := args[len(args)-1]
	stdout, err := respond(opts, script)
	if err != nil {
		fail(err.Error())
	}
	if opts.maxStreamBytes > 0 && int64(len(stdout)) > opts.maxStreamBytes {
		fail(streamEOF)
	}
	if _, err := os.Stdout.Write(stdout); err != nil {
		fail("podexec: " + err.Error())
	}
}

func respond(opts options, script string) ([]byte, error) {
	switch {
	case strings.Contains(script, wholePayloadRequest):
		return respondWithWholePayload(opts, script)
	case strings.Contains(script, `if [ -d "$target" ]`):
		return respondToProbe(opts, script)
	case discardScript.MatchString(script):
		// `rm -f` on a path that is already gone is not an error.
		err := os.Remove(hostPath(opts.root, discardScript.FindStringSubmatch(script)[1]))
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		return nil, nil
	case rangeRequest.MatchString(script):
		return respondToRange(opts, script)
	}
	return nil, fmt.Errorf("podexec: unrecognized script:\n%s", script)
}

// respondToProbe answers what the pod would send: the entry kind, the payload's
// length, the path to read it from, its digest, and the payload's first range.
func respondToProbe(opts options, script string) ([]byte, error) {
	target := probeScript.FindStringSubmatch(script)
	if target == nil {
		return nil, fmt.Errorf("podexec: no target in the probe script:\n%s", script)
	}
	podPath := target[1]
	info, err := os.Stat(hostPath(opts.root, podPath))
	if err != nil {
		return nil, fmt.Errorf("erun-outputs: not found: %s", podPath)
	}
	kind, staged := "file", podPath
	if info.IsDir() {
		if staged, err = stageArchive(opts.root, podPath); err != nil {
			return nil, err
		}
		kind = "dir"
	}
	payload, err := os.ReadFile(hostPath(opts.root, staged))
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(payload)
	head, err := encodeRange(opts, payload, 0, requestedLength(script))
	if err != nil {
		return nil, err
	}
	metadata := fmt.Sprintf("%s\n%d\n%s\n%s\n", kind, len(payload), staged, hex.EncodeToString(digest[:]))
	return append([]byte(metadata), head...), nil
}

// respondWithWholePayload answers the pre-range protocol: a kind marker and the
// entire payload, base64, in one stream.
func respondWithWholePayload(opts options, script string) ([]byte, error) {
	target := probeScript.FindStringSubmatch(script)
	if target == nil {
		return nil, fmt.Errorf("podexec: no target in the script:\n%s", script)
	}
	podPath := target[1]
	info, err := os.Stat(hostPath(opts.root, podPath))
	if err != nil {
		return nil, fmt.Errorf("erun-outputs: not found: %s", podPath)
	}
	kind, payload := "file", []byte(nil)
	if info.IsDir() {
		kind = "dir"
		payload, err = tarGz(hostPath(opts.root, podPath), filepath.Base(podPath))
	} else {
		payload, err = os.ReadFile(hostPath(opts.root, podPath))
	}
	if err != nil {
		return nil, err
	}
	return []byte(kind + "\n" + wrapBase64(payload) + "\n"), nil
}

func respondToRange(opts options, script string) ([]byte, error) {
	match := rangeRequest.FindStringSubmatch(script)
	offset, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("podexec: unreadable offset %q", match[1])
	}
	podPath := match[2]
	if podPath == "" {
		podPath = match[3]
	}
	if listsOffset(opts.failAlwaysAt, offset-1) {
		return nil, fmt.Errorf("%s", streamEOF)
	}
	if fired, err := consumeFailOnce(opts, offset-1); err != nil {
		return nil, err
	} else if fired {
		return nil, fmt.Errorf("%s", streamEOF)
	}
	payload, err := os.ReadFile(hostPath(opts.root, podPath))
	if err != nil {
		return nil, err
	}
	return encodeRange(opts, payload, offset-1, requestedLength(script))
}

// encodeRange frames one range the way the pod's own pipeline does: an encoding
// marker line, then base64 of the bytes (compressed first when the pod would
// have had gzip).
func encodeRange(opts options, payload []byte, offset, length int64) ([]byte, error) {
	end := offset + length
	if end > int64(len(payload)) {
		end = int64(len(payload))
	}
	if offset > end {
		offset = end
	}
	body := payload[offset:end]
	marker := "raw"
	if opts.encoding == "gzip" {
		marker = "gzip"
		var buffer bytes.Buffer
		// The pod compresses; how well it compresses is not what a scenario
		// asserts, and scanning for matches it will not find is the single
		// most expensive thing this emulator would otherwise do.
		writer, err := gzip.NewWriterLevel(&buffer, gzip.NoCompression)
		if err != nil {
			return nil, err
		}
		if _, err := writer.Write(body); err != nil {
			return nil, err
		}
		if err := writer.Close(); err != nil {
			return nil, err
		}
		body = buffer.Bytes()
	}
	return []byte(marker + "\n" + wrapBase64(body) + "\n"), nil
}

// wrapBase64 breaks the encoding at 76 columns the way base64(1) does, so a
// scenario's stream size matches what the real pipeline would have produced.
func wrapBase64(body []byte) string {
	encoded := base64.StdEncoding.EncodeToString(body)
	var wrapped strings.Builder
	wrapped.Grow(len(encoded) + len(encoded)/76 + 1)
	for len(encoded) > 76 {
		wrapped.WriteString(encoded[:76])
		wrapped.WriteByte('\n')
		encoded = encoded[76:]
	}
	wrapped.WriteString(encoded)
	return wrapped.String()
}

func requestedLength(script string) int64 {
	match := rangeRequest.FindStringSubmatch(script)
	if match == nil {
		return 0
	}
	length, _ := strconv.ParseInt(match[4], 10, 64)
	return length
}

// stageArchive writes the throwaway tarball a directory download reads from,
// where the pod's own mktemp would have put it.
func stageArchive(root, podPath string) (string, error) {
	if err := os.MkdirAll(filepath.Join(root, "tmp"), 0o755); err != nil {
		return "", err
	}
	staged, err := os.CreateTemp(filepath.Join(root, "tmp"), "erun-outputs-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = staged.Close() }()
	archive, err := tarGz(hostPath(root, podPath), filepath.Base(podPath))
	if err != nil {
		return "", err
	}
	if _, err := staged.Write(archive); err != nil {
		return "", err
	}
	return "/tmp/" + filepath.Base(staged.Name()), nil
}

func tarGz(hostDir, name string) ([]byte, error) {
	entries, err := os.ReadDir(hostDir)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	var buffer bytes.Buffer
	zip := gzip.NewWriter(&buffer)
	writer := tar.NewWriter(zip)
	if err := writer.WriteHeader(&tar.Header{Name: name + "/", Mode: 0o755, Typeflag: tar.TypeDir, ModTime: tarEntryEpoch}); err != nil {
		return nil, err
	}
	for _, entry := range entries {
		body, err := os.ReadFile(filepath.Join(hostDir, entry.Name()))
		if err != nil {
			return nil, err
		}
		header := &tar.Header{Name: name + "/" + entry.Name(), Mode: 0o644, Size: int64(len(body)), ModTime: tarEntryEpoch}
		if err := writer.WriteHeader(header); err != nil {
			return nil, err
		}
		if _, err := writer.Write(body); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	if err := zip.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// consumeFailOnce reports whether this offset is due to break the stream, and
// records that it has, so a scenario can prove the download recovers by
// re-reading the same range rather than by never being tested on one.
func consumeFailOnce(opts options, offset int64) (bool, error) {
	if opts.statePath == "" || !listsOffset(opts.failOnceAt, offset) {
		return false, nil
	}
	spent, err := os.ReadFile(opts.statePath)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	marker := strconv.FormatInt(offset, 10) + "\n"
	if strings.Contains(string(spent), marker) {
		return false, nil
	}
	return true, os.WriteFile(opts.statePath, append(spent, marker...), 0o644)
}

func listsOffset(offsets string, offset int64) bool {
	if strings.TrimSpace(offsets) == "" {
		return false
	}
	for _, field := range strings.Split(offsets, ",") {
		if strings.TrimSpace(field) == strconv.FormatInt(offset, 10) {
			return true
		}
	}
	return false
}

func hostPath(root, podPath string) string {
	return filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(podPath, "/")))
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
