package model

// WorkspaceSyncResult is what one pass changed.
type WorkspaceSyncResult struct {
	FilesCopied     int
	FilesDeleted    int
	ArtifactsCopied int
}
