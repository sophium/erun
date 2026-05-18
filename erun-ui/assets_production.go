//go:build production

package main

import (
	"embed"
	"io/fs"
)

//go:embed all:frontend/dist
var assets embed.FS

// frontendDistFS returns the embedded frontend bundle rooted at frontend/dist
// so headless mode can serve the same files Wails serves over its asset
// server. Errors from fs.Sub here would indicate a broken embed directive at
// compile time, so the headless server logs and serves an empty FS if that
// ever happens — the dev/production parity is verified by build tags.
func frontendDistFS() fs.FS {
	sub, err := fs.Sub(assets, "frontend/dist")
	if err != nil {
		return assets
	}
	return sub
}
