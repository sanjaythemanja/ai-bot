// Package web embeds the built UI (from ui/, via `make ui`) into the binary.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var files embed.FS

// FS returns the UI files rooted at dist/.
func FS() fs.FS {
	sub, err := fs.Sub(files, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
