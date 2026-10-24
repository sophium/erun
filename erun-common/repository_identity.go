package eruncommon

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

// ResolveReviewRepository answers which repository a review belongs to: the
// remote the caller named, or the checkout they are standing in when they
// named none. A command that always runs against the repository the operator
// is in should not make them retype its remote to be usable at all.
//
// It is an error, not an empty answer, when neither names a repository: a
// review whose repository cannot be recorded cannot be placed in any
// repository's merge queue, and creating one anyway is the dead end this
// resolution exists to prevent.
func ResolveReviewRepository(ctx core.Context, remoteURL, traceLabel string) (string, error) {
	remote := strings.TrimSpace(remoteURL)
	if remote == "" {
		ctx.Trace(traceLabel + ": no repository given; reading origin from the current checkout")
		origin, err := originRemoteURL()
		if err != nil {
			ctx.Trace(traceLabel + ": this checkout has no origin remote")
			return "", fmt.Errorf("a repository is required to open a review: pass --repository with the repository's remote, or run this from a clone of it")
		}
		remote = origin
		ctx.Trace(traceLabel + ": origin = " + remote)
	}
	identity, err := model.RepositoryIdentity(remote)
	if err != nil {
		ctx.Trace(traceLabel + ": repository resolution failed: " + err.Error())
		return "", err
	}
	ctx.Trace(traceLabel + ": repository = " + identity)
	return identity, nil
}
