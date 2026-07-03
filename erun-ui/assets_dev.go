//go:build !production

package main

import (
	"io/fs"
	"os"
)

var assets fs.FS = os.DirFS("frontend/dist")

// A non-production build normally serves from the vite dev server, but
// `--headless` has no such server and still needs a concrete FS to serve.
func frontendDistFS() fs.FS {
	return assets
}
