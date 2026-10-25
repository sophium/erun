package helmargs

const (
	WorktreeStorageHost = "host"
	WorktreeStoragePVC  = "pvc"
	// WorktreeStorageNone is informational for runtime envs; the chart treats it as "skip worktree mount".
	WorktreeStorageNone = "none"
)
