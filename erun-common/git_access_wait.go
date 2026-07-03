package eruncommon

import "time"

var remoteRepositoryAccessRetryInterval = 2 * time.Second

// WaitForGitAccess gives init and doctor one consistent SSH-key-import wait,
// so the user sees the same flow whichever side of the runtime pod drives it.
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
