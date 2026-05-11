package eruncommon

import "time"

// remoteRepositoryAccessRetryInterval is shared by init and doctor for
// the SSH key import polling loop.
//
// (Declared as a var, not a const, so test-time injection of a
// SleepFunc keeps the production cadence visible in one place.)
var remoteRepositoryAccessRetryInterval = 2 * time.Second

// WaitForGitAccess polls verifier until it returns nil, emitting the
// same "waiting / retrying / confirmed" trace lines init has always
// emitted. Both `erun init --remote` (kubectl-driven verify) and
// `erun doctor --finish-remote-init` (local `git ls-remote` verify)
// call this so the user sees a single, consistent SSH-key-import flow
// regardless of which side of the runtime pod they're driving it from.
//
// sleep is the cadence between attempts. Pass time.Sleep in production
// and a recording stub in tests. A nil sleep disables the pause —
// useful for tests that want the loop to spin without real waits.
func WaitForGitAccess(ctx Context, sleep SleepFunc, verifier func() error) error {
	ctx.Info("Waiting for the SSH key to be deployed to the git host. Rechecking every 2 seconds. Press Ctrl+C to cancel.")
	for attempts := 0; ; attempts++ {
		if err := verifier(); err == nil {
			if attempts > 0 {
				ctx.Info("Remote repository access confirmed.")
			}
			return nil
		}
		ctx.Info("SSH key not active yet; retrying in 2 seconds...")
		if sleep != nil {
			sleep(remoteRepositoryAccessRetryInterval)
		}
	}
}
