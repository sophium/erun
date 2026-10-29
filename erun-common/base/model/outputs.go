package model

import (
	"time"
)

type OutputEntry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
	IsDir   bool      `json:"isDir"`
}

// RuntimeOutputsListResult is one pod directory's entries, newest-first.
type RuntimeOutputsListResult struct {
	Dir       string        `json:"dir"`
	Entries   []OutputEntry `json:"entries"`
	Total     int           `json:"total"`
	Truncated bool          `json:"truncated"`
}

// RuntimeOutputResult is one downloaded entry; a directory arrives as a gzip
// tarball. Bytes stays out of JSON so transports can encode it separately.
type RuntimeOutputResult struct {
	Name          string `json:"name"`
	IsArchive     bool   `json:"isArchive"`
	ArchiveFormat string `json:"archiveFormat,omitempty"`
	Size          int64  `json:"size"`
	SHA256        string `json:"sha256"`
	Bytes         []byte `json:"-"`
	// Signing is nil unless host-side ad-hoc signing had something to report.
	Signing *HostArtifactSigning `json:"signing,omitempty"`
}

// RuntimeOutputsParams selects which directory `outputs list` reads.
type RuntimeOutputsParams struct {
	// Dir defaults to DefaultRuntimeOutputsDir.
	Dir string
	// Limit caps entries returned; 0 means no limit.
	Limit int
}

// RuntimeOutputDownloadParams selects which entry `outputs download` fetches.
type RuntimeOutputDownloadParams struct {
	// Dir defaults to DefaultRuntimeOutputsDir.
	Dir string
	// Name is a single path segment under Dir.
	Name string
}
