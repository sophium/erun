//go:build production

package main

import (
	"embed"
	"io/fs"
)

//go:embed all:frontend/dist
var assets embed.FS

// frontendDistFS returns the embedded frontend bundle rooted at frontend/dist so
// headless mode serves the same files Wails serves over its asset server. fs.Sub
// only fails on a broken embed directive, which surfaces at compile time, so the
// error path falls back to the full embed FS.
func frontendDistFS() fs.FS {
	sub, err := fs.Sub(assets, "frontend/dist")
	if err != nil {
		return assets
	}
	return sub
}
