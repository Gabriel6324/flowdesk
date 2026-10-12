package webassets

import (
	"embed"
	"io/fs"
)

//go:embed dist
var content embed.FS

func FS() fs.FS {
	f, e := fs.Sub(content, "dist")
	if e != nil {
		panic(e)
	}
	return f
}
