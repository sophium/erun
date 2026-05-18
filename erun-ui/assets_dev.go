//go:build !production

package main

import (
	"io/fs"
	"os"
)

var assets fs.FS = os.DirFS("frontend/dist")

// frontendDistFS returns the dev-mode frontend bundle. Wails normally serves
// straight from the vite dev server in non-production builds, but `--headless`
// still needs a concrete FS to serve, so we use the same on-disk dist
// directory the production embed mirrors.
func frontendDistFS() fs.FS {
	return assets
}
