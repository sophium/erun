package eruncommon

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// Spinner emits a TTY-aware live status line on the given writer (typically
// stderr). When the writer is not a terminal (integration tests, redirected
// output, CI logs, NO_COLOR or TERM=dumb set), Start returns nil and the
// nil-receiver Stop is a no-op. That keeps integration goldens deterministic
// while giving interactive users a visible "still working" signal during
// long-running operations like helm install --wait.
type Spinner struct {
	out      io.Writer
	message  string
	started  time.Time
	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

const spinnerFrames = `|/-\`

// StartSpinner begins ticking a spinner with the given message on out, or
// returns nil when out is not a terminal. Callers should defer (*Spinner).Stop
// in the same scope as Start.
func StartSpinner(out io.Writer, message string) *Spinner {
	if !writerIsTerminalForSpinner(out) {
		return nil
	}
	s := &Spinner{
		out:     out,
		message: strings.TrimSpace(message),
		started: time.Now(),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	go s.run()
	return s
}

func (s *Spinner) run() {
	defer close(s.done)
	ticker := time.NewTicker(120 * time.Millisecond)
	defer ticker.Stop()
	frame := 0
	s.draw(frame)
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			frame++
			s.draw(frame)
		}
	}
}

func (s *Spinner) draw(frame int) {
	elapsed := time.Since(s.started).Round(time.Second)
	line := fmt.Sprintf("\r %c %s (%s)", spinnerFrames[frame%len(spinnerFrames)], s.message, elapsed)
	_, _ = io.WriteString(s.out, line)
}

// Stop ends the spinner, erases its line, and returns the wall-clock duration
// the spinner was active. Safe to call on nil.
func (s *Spinner) Stop() time.Duration {
	if s == nil {
		return 0
	}
	var elapsed time.Duration
	s.stopOnce.Do(func() {
		elapsed = time.Since(s.started)
		close(s.stop)
		<-s.done
		_, _ = io.WriteString(s.out, "\r"+strings.Repeat(" ", spinnerLineWidth)+"\r")
	})
	return elapsed
}

const spinnerLineWidth = 80

func writerIsTerminalForSpinner(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if strings.EqualFold(os.Getenv("TERM"), "dumb") {
		return false
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}
