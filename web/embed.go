package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist foundation
var content embed.FS

func Assets() fs.FS {
	sub, err := fs.Sub(content, "dist")
	if err != nil {
		panic(err)
	}
	if _, err = fs.Stat(sub, "index.html"); err != nil {
		sub, err = fs.Sub(content, "foundation")
		if err != nil {
			panic(err)
		}
	}
	return sub
}
