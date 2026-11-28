//go:build !unit1

package main

import (
	"github.com/aiviarium/heron/internal/core/registry"
	"github.com/aiviarium/heron/internal/tools/docs"
	"github.com/aiviarium/heron/internal/tools/lsp"
	"github.com/aiviarium/heron/internal/tools/prose"
	"github.com/aiviarium/heron/internal/tools/server"
)

// 2. Development tools.
func init() {
	units = append(units,
		prose.Register, // Vale and the spec-aware prose lints
		lsp.Register,   // `heron lsp`
		docs.Register,  // the docs renderer and `heron generate docs`
		func(r *registry.Registry) { server.Register(r) }, // `heron serve`: the spec browser and the preview
	)
}
