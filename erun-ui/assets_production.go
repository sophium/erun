//go:build production

package main

import (
	"embed"
	"io/fs"
)

//go:embed all:frontend/dist
var assets embed.FS

func frontendDistFS() fs.FS {
	sub, err := fs.Sub(assets, "frontend/dist")
	if err != nil {
		return assets
	}
	return sub
}
