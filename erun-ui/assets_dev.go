//go:build !production

package main

import (
	"io/fs"
	"os"
)

var assets fs.FS = os.DirFS("frontend/dist")
