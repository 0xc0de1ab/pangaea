// Package routerui exposes the embedded React dashboard build output.
package routerui

import (
	"embed"
	"io/fs"
)

//go:embed dist
var embeddedDist embed.FS

// Dist returns the embedded router dashboard distribution filesystem.
func Dist() fs.FS {
	dist, err := fs.Sub(embeddedDist, "dist")
	if err != nil {
		panic("routerui: embedded dist is unavailable: " + err.Error())
	}
	return dist
}
