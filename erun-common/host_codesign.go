package eruncommon

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// arm64 macOS refuses to exec a Mach-O that carries no code signature at all:
// the kernel SIGKILLs it before main, with no error text and no dialog, so a
// darwin binary cross-built in a Linux pod reads as a truncated or crashed file
// rather than an unsigned one. codesign exists only on macOS, so the signature
// cannot come from the build — it has to come from the host-side step that turns
// an artifact into something the operator runs.

const (
	// machOHeaderProbeBytes covers the 4-byte magic plus the universal header's
	// architecture count. The count is what separates a fat Mach-O from a Java
	// class file: both open with 0xCAFEBABE, but a class file's next four bytes
	// are its version (>= 45), well past any plausible architecture count.
	machOHeaderProbeBytes = 8
	machOMaxFatArch       = 32
)

// machOThinMagics are the on-disk magic bytes of a single-architecture Mach-O,
// in both byte orders and both widths.
var machOThinMagics = [][4]byte{
	{0xFE, 0xED, 0xFA, 0xCE},
	{0xFE, 0xED, 0xFA, 0xCF},
	{0xCE, 0xFA, 0xED, 0xFE},
	{0xCF, 0xFA, 0xED, 0xFE},
}

// LocalCodesignIdentity is the self-signed identity erun keeps for locally
// built macOS artifacts. macOS pins a privacy grant to the identity that signed
// the code; an ad-hoc signature has none, so TCC pins the code-directory hash
// instead and every rebuild silently drops the grant. erun-ui/codesign.sh
// creates the identity on a developer's first desktop build, and everything
// that signs a local artifact adopts it so the two cannot disagree.
const LocalCodesignIdentity = "ERun Local Development"

// LocalCodesignKeychainFile is the keychain erun-ui/codesign.sh creates for the
// local identity, under the user's Library/Keychains. A keychain of erun's own
// rather than the login one is what keeps creating and using the identity free
// of GUI authorization prompts, which a build driven by an agent cannot answer.
const LocalCodesignKeychainFile = "erun-local-signing.keychain-db"

// LocalCodesignKeychainPassword is not a secret. `security create-keychain`
// demands a password, and this keychain holds one self-signed local identity
// that authenticates nothing to anyone.
const LocalCodesignKeychainPassword = "erun-local-signing"

// HostArtifactSigning is what signing did to one artifact on this host. Note
// carries the operator-facing diagnostic and is set only when something needs
// saying; it never means the artifact failed to arrive. Identity names the
// signer when a stable one was available, and is empty for an ad-hoc signature.
type HostArtifactSigning struct {
	Path     string `json:"path"`
	Signed   bool   `json:"signed"`
	Identity string `json:"identity,omitempty"`
	Note     string `json:"note,omitempty"`
}

// Describe renders the outcome as the single line a transport shows the
// operator, or "" when nothing happened worth reporting. It names the signer,
// because which identity an artifact carries is what decides whether a privacy
// grant it is given survives the next build.
func (s HostArtifactSigning) Describe() string {
	if s.Note != "" {
		return s.Note
	}
	if !s.Signed {
		return ""
	}
	if s.Identity != "" {
		return "Signed " + s.Path + " as " + s.Identity + " so macOS will run it and its privacy grants survive a rebuild"
	}
	return "Ad-hoc signed " + s.Path + " so macOS will run it"
}

// SignHostArtifact signs an unsigned Mach-O that has just landed on this host,
// and leaves everything else alone: a non-darwin host, a file whose
// content is not Mach-O, and a file that already carries a signature are all
// no-ops. A Mach-O it recognises also gets its execute bit, because an artifact
// arrives 0644 from a download and 0444 from the read-only mirror and neither
// can be exec'd.
//
// Signing trouble is reported through Note rather than as an error: the artifact
// is on disk either way, and a diagnosable state beats the silent kill.
func SignHostArtifact(path string) HostArtifactSigning {
	signing := HostArtifactSigning{Path: path}
	if DetectHost().OS != HostOSDarwin {
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

// signWhileWritable lends the write bit to codesign, which rewrites the file in
// place. The workspace mirror deliberately keeps its artifacts read-only, and a
// mirrored artifact is exactly the darwin binary that needs signing, so the
// signature must not depend on the mirror relaxing first. The original mode is
// restored either way.
//
// It prefers the host's stable local identity and falls back to ad-hoc, so a
// binary that lands here carries the same signer a locally built desktop does
// and any privacy grant given to it survives the next build. Ad-hoc remains the
// floor: the point of signing at all is that macOS SIGKILLs an unsigned Mach-O.
func signWhileWritable(path string) (string, string, error) {
	if info, err := os.Stat(path); err == nil && info.Mode().Perm()&0o200 == 0 {
		mode := info.Mode().Perm()
		if chmodErr := os.Chmod(path, mode|0o200); chmodErr == nil {
			defer func() { _ = os.Chmod(path, mode) }()
		}
	}
	if keychain := localCodesignKeychain(); keychain != "" {
		// The keychain relocks across a reboot, and a locked one fails every
		// signature. Its password is erun's own, so this needs no operator.
		_, _ = Command("security", "unlock-keychain", "-p", LocalCodesignKeychainPassword, keychain).CombinedOutput()
		if output, err := runHostCodesign("-s", LocalCodesignIdentity, "--keychain", keychain, "-f", path); err == nil {
			return LocalCodesignIdentity, output, nil
		}
	}
	output, err := runHostCodesign("-s", "-", "-f", path)
	return "", output, err
}

// localCodesignKeychain is the keychain erun-ui/codesign.sh creates, or "" when
// this host has no local identity yet. Presence of the file is the whole test:
// a keychain that turns out not to hold the identity fails the signing call,
// which falls back to ad-hoc and reports what it did.
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

// hostArtifactSigningSummary folds a batch of signings into the counts one
// bounded log line can carry, so a mirror pass reports what it signed without
// emitting a line per file.
type hostArtifactSigningSummary struct {
	signed int
	note   string
}

// signHostArtifacts signs every artifact a mirror pass just materialised. It
// keeps the first diagnostic so a batch that could not be signed still says why.
func signHostArtifacts(paths []string) hostArtifactSigningSummary {
	var summary hostArtifactSigningSummary
	for _, path := range paths {
		signing := SignHostArtifact(path)
		if signing.Signed {
			summary.signed++
		}
		if summary.note == "" {
			summary.note = signing.Note
		}
	}
	return summary
}

// fileIsMachO classifies by content, never by name: an artifacts directory holds
// tarballs, text, and foreign-OS binaries that codesign has no business seeing.
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

// makeHostArtifactExecutable mirrors each read bit into the matching execute
// bit, so a downloaded 0644 artifact becomes 0755 and a read-only mirrored 0444
// one becomes 0555 without widening who can reach it.
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

// hostArtifactIsSigned asks codesign what the file already carries. A non-zero
// exit is codesign's way of saying "not signed at all"; anything else (a missing
// or unusable codesign) is a state the operator has to be told about.
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
	output, err := Command("codesign", args...).CombinedOutput()
	return strings.TrimSpace(string(output)), err
}

// unsignedMachONote states the failure and the one command that repairs it, so
// an operator who hits the silent SIGKILL later has already been told why.
func unsignedMachONote(path, what string, err error, output string) string {
	note := fmt.Sprintf("%s is an unsigned Mach-O and %s: %v", path, what, err)
	if trimmed := strings.TrimSpace(output); trimmed != "" {
		note += " (" + trimmed + ")"
	}
	return note + "; macOS kills an unsigned Mach-O with SIGKILL and no message, so sign it by hand with: codesign -s - -f " + path
}
