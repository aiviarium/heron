package server

import (
	"embed"
	"io/fs"
)

// The built frontend: `pnpm --filter @heron/shell build` writes it to
// frontend/dist, which isn't committed. frontend/keep.txt keeps the pattern
// matching in a fresh clone, where the binary then serves no app.
//
//go:embed all:frontend
var frontend embed.FS

// Frontend returns the embedded frontend's files.
func Frontend() fs.FS {
	sub, err := fs.Sub(frontend, "frontend/dist")
	if err != nil {
		panic(err)
	}
	return sub
}
