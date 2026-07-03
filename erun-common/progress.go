package eruncommon

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// Spinner gives interactive users a "still working" signal during long
// operations like helm install --wait. It is nil when out is not a terminal, so
// redirected and CI output stay deterministic; the nil receiver's Stop is a safe
// no-op.
type Spinner struct {
	out      io.Writer
	message  string
	started  time.Time
	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

const spinnerFrames = `|/-\`

// StartSpinner starts a spinner on out, returning nil when out is not a terminal.
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

// Stop ends the spinner and returns how long it was active. Safe to call on nil.
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
