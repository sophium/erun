package model

import (
	"time"
)

// ConfigBackup is one dated backup file; Date comes from the filename, not mtime.
type ConfigBackup struct {
	Path string
	Date time.Time
}
