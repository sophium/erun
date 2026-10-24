package review

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
)

// ResolveReviewRepository returns the named remote, else the checkout's origin.
// Naming neither is an error: a review without a repository can never be queued.
func ResolveReviewRepository(ctx core.Context, remoteURL, traceLabel string) (string, error) {
	remote := strings.TrimSpace(remoteURL)
	if remote == "" {
		ctx.Trace(traceLabel + ": no repository given; reading origin from the current checkout")
		origin, err := OriginRemoteURL()
		if err != nil {
			ctx.Trace(traceLabel + ": this checkout has no origin remote")
			return "", fmt.Errorf("a repository is required to open a review: pass --repository with the repository's remote, or run this from a clone of it")
		}
		remote = origin
		ctx.Trace(traceLabel + ": origin = " + remote)
	}
	identity, err := core.RepositoryIdentity(remote)
	if err != nil {
		ctx.Trace(traceLabel + ": repository resolution failed: " + err.Error())
		return "", err
	}
	ctx.Trace(traceLabel + ": repository = " + identity)
	return identity, nil
}
