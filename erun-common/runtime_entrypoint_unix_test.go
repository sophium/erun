//go:build !windows

package eruncommon

func (b *syncBuffer) snapshot() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}
