//go:build !production

package main

import (
	"io/fs"
	"os"
)

var assets fs.FS = os.DirFS("frontend/dist")

// `--headless` has no vite dev server and still needs a concrete FS.
func frontendDistFS() fs.FS {
	return assets
}
