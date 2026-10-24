package codesign

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

// arm64 macOS SIGKILLs an unsigned Mach-O before main; codesign exists only on
// macOS, so signing happens on the host, not in the pod build.

const (
	// machOHeaderProbeBytes includes the arch count, which tells a fat Mach-O from a
	// Java class file (both start 0xCAFEBABE).
	machOHeaderProbeBytes = 8
	machOMaxFatArch       = 32
)

var machOThinMagics = [][4]byte{
	{0xFE, 0xED, 0xFA, 0xCE},
	{0xFE, 0xED, 0xFA, 0xCF},
	{0xCE, 0xFA, 0xED, 0xFE},
	{0xCF, 0xFA, 0xED, 0xFE},
}

// LocalCodesignIdentity is erun's stable local signing identity; ad-hoc signatures
// make TCC drop privacy grants on every rebuild. erun-ui/codesign.sh creates it.
const LocalCodesignIdentity = "ERun Local Development"

// LocalCodesignKeychainFile is a dedicated keychain, which avoids GUI authorization prompts.
const LocalCodesignKeychainFile = "erun-local-signing.keychain-db"

// LocalCodesignKeychainPassword is not a secret; the keychain authenticates nothing.
const LocalCodesignKeychainPassword = "erun-local-signing"

// SignHostArtifact signs an unsigned Mach-O on a darwin host and makes it executable;
// anything else is a no-op. Failures go in Note because the file is on disk either way.
func SignHostArtifact(path string) model.HostArtifactSigning {
	signing := model.HostArtifactSigning{Path: path}
	if core.DetectHost().OS != model.HostOSDarwin {
		return signing
	}
	isMachO, err := fileIsMachO(path)
	if err != nil {
		signing.Note = fmt.Sprintf("could not check %s for a Mach-O header: %v", path, err)
		return signing
	}
	if !isMachO {
		return signing
	}
	if err := makeHostArtifactExecutable(path); err != nil {
		signing.Note = fmt.Sprintf("could not make %s executable: %v", path, err)
		return signing
	}
	alreadySigned, output, err := hostArtifactIsSigned(path)
	if err != nil {
		signing.Note = unsignedMachONote(path, "could not check whether it is already signed", err, output)
		return signing
	}
	if alreadySigned {
		return signing
	}
	identity, output, err := signWhileWritable(path)
	if err != nil {
		signing.Note = unsignedMachONote(path, "ad-hoc signing it failed", err, output)
		return signing
	}
	signing.Signed = true
	signing.Identity = identity
	return signing
}

// signWhileWritable restores the mode afterwards, since mirrored artifacts are read-only.
// It prefers the local identity so privacy grants survive rebuilds, falling back to ad-hoc.
func signWhileWritable(path string) (string, string, error) {
	if info, err := os.Stat(path); err == nil && info.Mode().Perm()&0o200 == 0 {
		mode := info.Mode().Perm()
		if chmodErr := os.Chmod(path, mode|0o200); chmodErr == nil {
			defer func() { _ = os.Chmod(path, mode) }()
		}
	}
	if keychain := localCodesignKeychain(); keychain != "" {
		// The keychain relocks across reboots.
		_, _ = core.Command("security", "unlock-keychain", "-p", LocalCodesignKeychainPassword, keychain).CombinedOutput()
		if output, err := runHostCodesign("-s", LocalCodesignIdentity, "--keychain", keychain, "-f", path); err == nil {
			return LocalCodesignIdentity, output, nil
		}
	}
	output, err := runHostCodesign("-s", "-", "-f", path)
	return "", output, err
}

// localCodesignKeychain returns "" when this host has no local identity yet.
func localCodesignKeychain() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	path := filepath.Join(home, "Library", "Keychains", LocalCodesignKeychainFile)
	if info, statErr := os.Stat(path); statErr != nil || info.IsDir() {
		return ""
	}
	return path
}

// HostArtifactSigningSummary aggregates a batch into one log line.
type HostArtifactSigningSummary struct {
	Signed int
	Note   string
}

// SignHostArtifacts keeps the first diagnostic so a failed batch still says why.
func SignHostArtifacts(paths []string) HostArtifactSigningSummary {
	var summary HostArtifactSigningSummary
	for _, path := range paths {
		signing := SignHostArtifact(path)
		if signing.Signed {
			summary.Signed++
		}
		if summary.Note == "" {
			summary.Note = signing.Note
		}
	}
	return summary
}

// fileIsMachO classifies by content, never by name.
func fileIsMachO(path string) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = file.Close() }()
	head := make([]byte, machOHeaderProbeBytes)
	read, err := io.ReadFull(file, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return false, err
	}
	return headerIsMachO(head[:read]), nil
}

func headerIsMachO(head []byte) bool {
	if len(head) < 4 {
		return false
	}
	magic := [4]byte(head[:4])
	for _, known := range machOThinMagics {
		if magic == known {
			return true
		}
	}
	if len(head) < machOHeaderProbeBytes {
		return false
	}
	switch magic {
	case [4]byte{0xCA, 0xFE, 0xBA, 0xBE}:
		return plausibleFatArchCount(binary.BigEndian.Uint32(head[4:8]))
	case [4]byte{0xBE, 0xBA, 0xFE, 0xCA}:
		return plausibleFatArchCount(binary.LittleEndian.Uint32(head[4:8]))
	}
	return false
}

func plausibleFatArchCount(count uint32) bool {
	return count >= 1 && count <= machOMaxFatArch
}

// makeHostArtifactExecutable mirrors each read bit into the execute bit.
func makeHostArtifactExecutable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	mode := info.Mode().Perm()
	desired := mode | ((mode & 0o444) >> 2)
	if desired == mode {
		return nil
	}
	return os.Chmod(path, desired)
}

// hostArtifactIsSigned treats codesign's non-zero exit as unsigned; other failures are reported.
func hostArtifactIsSigned(path string) (bool, string, error) {
	output, err := runHostCodesign("-d", path)
	if err == nil {
		return true, output, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return false, output, nil
	}
	return false, output, err
}

func runHostCodesign(args ...string) (string, error) {
	output, err := core.Command("codesign", args...).CombinedOutput()
	return strings.TrimSpace(string(output)), err
}

func unsignedMachONote(path, what string, err error, output string) string {
	note := fmt.Sprintf("%s is an unsigned Mach-O and %s: %v", path, what, err)
	if trimmed := strings.TrimSpace(output); trimmed != "" {
		note += " (" + trimmed + ")"
	}
	return note + "; macOS kills an unsigned Mach-O with SIGKILL and no message, so sign it by hand with: codesign -s - -f " + path
}
