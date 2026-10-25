package secretstore

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestFileCloudSecretStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := NewFileCloudSecretStore(filepath.Join(dir, "cloud-secrets"))
	const ref = "cloudflare/ci+acct@cloudflare"

	mustNoErr(t, store.SaveCloudSecret(ref, "tok-value"), "save")
	got, err := store.LoadCloudSecret(ref)
	mustNoErr(t, err, "load")
	if got != "tok-value" {
		t.Fatalf("load = %q, want tok-value", got)
	}

	// Windows has no POSIX mode bits, so a 0600 file reports 0666 there.
	entries, err := os.ReadDir(filepath.Join(dir, "cloud-secrets"))
	mustNoErr(t, err, "readdir")
	if len(entries) != 1 {
		t.Fatalf("expected one secret file, got %d", len(entries))
	}
	info, err := entries[0].Info()
	mustNoErr(t, err, "stat")
	wantPerm := os.FileMode(0o600)
	if runtime.GOOS == "windows" {
		wantPerm = 0o666
	}
	if perm := info.Mode().Perm(); perm != wantPerm {
		t.Fatalf("secret file perm = %o, want %o", perm, wantPerm)
	}

	mustNoErr(t, store.DeleteCloudSecret(ref), "delete")
	mustNoErr(t, store.DeleteCloudSecret(ref), "delete missing")
	if _, err := store.LoadCloudSecret(ref); err == nil {
		t.Fatal("expected load after delete to fail")
	}
}

func mustNoErr(t *testing.T, err error, what string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}
