package linuxpkgcheck

import (
	"sync"
)

// syncBuffer lets the test goroutine poll for the sign-in URL the login
// printed while the login's own goroutine concurrently writes it.
type syncBuffer struct {
	mu   sync.Mutex
	data []byte
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, p...)
	return len(p), nil
}
