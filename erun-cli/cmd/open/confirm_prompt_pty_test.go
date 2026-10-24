//go:build linux

package open

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

	"github.com/sophium/erun/cmd/shared/prompt"
	"golang.org/x/term"
)

const (
	ioctlTIOCSPTLCK = 0x40045431 // unlock the pty master's slave
	ioctlTIOCGPTN   = 0x80045430 // read the slave's number
)

// openTestPTY returns a real terminal pair; only a pty makes term.IsTerminal true.
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

// The alias confirm writes through an adapter over stderr; echo suppression must see
// through it to the terminal, or the settled line prints again.
func TestSettledPromptSuppressesTheEchoBehindTheAliasConfirmsWriter(t *testing.T) {
	t.Setenv("ERUN_FORCE_TTY", "1")
	master, slave := openTestPTY(t)
	if !term.IsTerminal(int(slave.Fd())) {
		t.Fatalf("the test pty's slave is not a terminal, so this test cannot reach the echo check")
	}
	// Raw mode disables the pty's echo, so the master reads exactly what the prompt wrote.
	if _, err := term.MakeRaw(int(slave.Fd())); err != nil {
		t.Fatalf("make the pty raw: %v", err)
	}

	previousStdin, previousInput := os.Stdin, prompt.PlainPromptInput
	os.Stdin = slave
	prompt.PlainPromptInput = sync.OnceValue(func() *bufio.Reader { return bufio.NewReader(slave) })
	t.Cleanup(func() { os.Stdin, prompt.PlainPromptInput = previousStdin, previousInput })

	var captured bytes.Buffer
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		_, _ = io.Copy(&captured, master)
	}()

	if _, err := io.WriteString(master, "y\n"); err != nil {
		t.Fatalf("type the answer: %v", err)
	}
	ok, err := prompt.ConfirmPromptTo(prompt.RunPrompt, "add team-dev to ~/.bashrc", nopWriteCloser{slave})
	if err != nil {
		t.Fatalf("confirmPromptTo: %v", err)
	}
	if !ok {
		t.Fatal("answering y must confirm")
	}
	// Closing the slave drains the reader, so the assertion waits on the copy, not a clock.
	if err := slave.Close(); err != nil {
		t.Fatalf("close the pty slave: %v", err)
	}
	<-drained

	rendered := confirmANSI.ReplaceAllString(captured.String(), "")
	if label := "? add team-dev to ~/.bashrc? [Y/n] "; !strings.Contains(rendered, label) {
		t.Fatalf("the confirm did not write its label to the alias confirm's writer; captured %q, want it to contain %q", rendered, label)
	}
	if settled := "add team-dev to ~/.bashrc? y"; strings.Contains(rendered, settled) {
		t.Fatalf("the confirm printed the settled line %q on top of an answer its own terminal already echoed, so the prompt settles on an extra blank line and a repeated answer:\n%q", settled, rendered)
	}
}
