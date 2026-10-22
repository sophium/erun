//go:build linux

package cmd

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/term"
)

const (
	ioctlTIOCSPTLCK = 0x40045431 // unlock the pty master's slave
	ioctlTIOCGPTN   = 0x80045430 // read the slave's number
)

// openTestPTY returns the master and slave of a real terminal. The alias
// confirm's echo suppression is decided by what the answer is read from and
// what the label is written to both being terminals, and only a pty makes
// term.IsTerminal true, so this regression cannot be reached through a pipe.
func openTestPTY(t *testing.T) (master, slave *os.File) {
	t.Helper()
	masterFD, err := syscall.Open("/dev/ptmx", syscall.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("open /dev/ptmx: %v", err)
	}
	var unlock int32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(masterFD), uintptr(ioctlTIOCSPTLCK), uintptr(unsafe.Pointer(&unlock))); errno != 0 {
		_ = syscall.Close(masterFD)
		t.Fatalf("unlock pty: %v", errno)
	}
	var number uint32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(masterFD), uintptr(ioctlTIOCGPTN), uintptr(unsafe.Pointer(&number))); errno != 0 {
		_ = syscall.Close(masterFD)
		t.Fatalf("read pty number: %v", errno)
	}
	slaveFD, err := syscall.Open(fmt.Sprintf("/dev/pts/%d", number), syscall.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		_ = syscall.Close(masterFD)
		t.Fatalf("open pty slave: %v", err)
	}
	master, slave = os.NewFile(uintptr(masterFD), "ptmx"), os.NewFile(uintptr(slaveFD), "pts")
	t.Cleanup(func() {
		_ = master.Close()
		_ = slave.Close()
	})
	return master, slave
}

// The alias confirm is the one confirmation that runs with both ends on a
// terminal, and it renders through promptStderr(): an adapter over the real
// stderr, because promptui takes an io.WriteCloser and must not be able to
// close the process's own stderr. The terminal echoes the answer
// (confirmPromptTo's prompt has no Default, so "y" is the whole answer) and its
// newline, which already terminates the label line -- so the settled line has to
// be suppressed.
//
// A suppression check that only recognizes a bare *os.File sees the adapter
// instead of the terminal it wraps, never fires, and prints the answer again
// under a blank line: three rendered lines where the repaint prompt left one.
func TestSettledPromptSuppressesTheEchoBehindTheAliasConfirmsWriter(t *testing.T) {
	t.Setenv("ERUN_FORCE_TTY", "1")
	master, slave := openTestPTY(t)
	if !term.IsTerminal(int(slave.Fd())) {
		t.Fatalf("the test pty's slave is not a terminal, so this test cannot reach the echo check")
	}
	// Raw mode turns the pty's own echo off, so everything read back from the
	// master is what the prompt wrote and the render can be counted exactly.
	if _, err := term.MakeRaw(int(slave.Fd())); err != nil {
		t.Fatalf("make the pty raw: %v", err)
	}

	previousStdin, previousInput := os.Stdin, plainPromptInput
	os.Stdin = slave
	plainPromptInput = sync.OnceValue(func() *bufio.Reader { return bufio.NewReader(slave) })
	t.Cleanup(func() { os.Stdin, plainPromptInput = previousStdin, previousInput })

	var captured bytes.Buffer
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		_, _ = io.Copy(&captured, master)
	}()

	if _, err := io.WriteString(master, "y\n"); err != nil {
		t.Fatalf("type the answer: %v", err)
	}
	// The writer is the adapter the alias flow builds, over the terminal the
	// answer is read from -- the state the report described.
	ok, err := confirmPromptTo(runPrompt, "add team-dev to ~/.bashrc", nopWriteCloser{slave})
	if err != nil {
		t.Fatalf("confirmPromptTo: %v", err)
	}
	if !ok {
		t.Fatal("answering y must confirm")
	}
	// Closing the slave ends the capture: the reader drains what is buffered and
	// then errors, so the assertion below waits on the copy finishing rather
	// than on a clock.
	if err := slave.Close(); err != nil {
		t.Fatalf("close the pty slave: %v", err)
	}
	<-drained

	rendered := confirmANSI.ReplaceAllString(captured.String(), "")
	if label := "? add team-dev to ~/.bashrc? [Y/n] "; !strings.Contains(rendered, label) {
		t.Fatalf("the confirm did not write its label to the alias confirm's writer; captured %q, want it to contain %q", rendered, label)
	}
	// The settled line is what the repaint prompt ends on and what the terminal's
	// echo already covers; printing it is the second and third line of the
	// report's three.
	if settled := "add team-dev to ~/.bashrc? y"; strings.Contains(rendered, settled) {
		t.Fatalf("the confirm printed the settled line %q on top of an answer its own terminal already echoed, so the prompt settles on an extra blank line and a repeated answer:\n%q", settled, rendered)
	}
}
