package model

// WriteWorkingTreeFileParams has no default Path, so content lands only where the caller chose.
type WriteWorkingTreeFileParams struct {
	// Path is absolute or relative to the working tree root.
	Path string
	// Content is written verbatim; nothing in it is interpreted.
	Content string
}

// WriteWorkingTreeFileResult is what the write actually did.
type WriteWorkingTreeFileResult struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
}
