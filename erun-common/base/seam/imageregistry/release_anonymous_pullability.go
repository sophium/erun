package imageregistry

import (
	"context"
	"net/http"
)

// AnonymousPullProbeFunc reports whether repoPath:tag pulls with no credential; injectable for tests.
type AnonymousPullProbeFunc func(ctx context.Context, client *http.Client, repoPath, tag string) (bool, error)
