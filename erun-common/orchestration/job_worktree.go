package orchestration

import (
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
)

func GitRemoteDefaultBranch(ctx core.Context, dir, remote string) (string, bool) {
	ctx.TraceCommand("", "git", "-C", dir, "symbolic-ref", "refs/remotes/"+remote+"/HEAD")
	output, err := core.Command("git", "-C", dir, "symbolic-ref", "refs/remotes/"+remote+"/HEAD").Output()
	if err != nil {
		return "", false
	}
	ref := strings.TrimSpace(string(output))
	prefix := "refs/remotes/" + remote + "/"
	if !strings.HasPrefix(ref, prefix) {
		return "", false
	}
	return strings.TrimPrefix(ref, prefix), true
}
