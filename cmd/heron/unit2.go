//go:build !unit1

package main

import (
	"github.com/helightdev/specs-v0/internal/core/registry"
	"github.com/helightdev/specs-v0/internal/tools/docs"
	"github.com/helightdev/specs-v0/internal/tools/lsp"
	"github.com/helightdev/specs-v0/internal/tools/prose"
	"github.com/helightdev/specs-v0/internal/tools/server"
)

// 2. Development tools.
func init() {
	units = append(units,
		prose.Register, // Vale and the spec-aware prose lints
		lsp.Register,   // `heron lsp`
		docs.Register,  // the docs renderer and `heron generate docs`
		func(r *registry.Registry) { server.Register(r) }, // `heron serve`, with the language server on /lsp
	)
}
