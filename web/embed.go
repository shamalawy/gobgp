// Package web embeds the built Angular application.
package web

import (
	"embed"
	"io/fs"
)

// dist is filled by `npm run build`; a fresh checkout holds only .gitkeep.
//
//go:embed all:dist
var dist embed.FS

// UI returns the built application, or nil when it has not been built.
func UI() fs.FS {
	ui, _ := fs.Sub(dist, "dist/app/browser")
	if _, err := fs.Stat(ui, "index.html"); err != nil {
		return nil
	}
	return ui
}
