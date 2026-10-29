package model

// UploadRuntimeInputParams selects the file `inputs upload` places in the runtime pod.
type UploadRuntimeInputParams struct {
	LocalPath string
	// RemotePath is never defaulted, so a file cannot land somewhere a
	// background sync reconciles away.
	RemotePath string
}
